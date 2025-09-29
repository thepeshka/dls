package main

import (
	"context"
	"dls/si"
	"errors"
	"fmt"
	"golang.org/x/time/rate"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

type RateLimitedIO struct {
	reader  io.Reader
	limiter *rate.Limiter
}

func NewRateLimitedIO(reader io.Reader, bytesPerSec int64) *RateLimitedIO {
	return &RateLimitedIO{
		reader:  reader,
		limiter: rate.NewLimiter(rate.Limit(bytesPerSec), int(bytesPerSec)),
	}
}

func (r *RateLimitedIO) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if err != nil {
		return n, err
	}

	// Wait for permission to read n bytes
	err = r.limiter.WaitN(context.Background(), n)
	return n, err
}

type FixedLengthReader struct {
	io.Reader
	length int
}

func NewFixedLengthReader(r io.Reader, l int) *FixedLengthReader {
	return &FixedLengthReader{r, l}
}

func (r *FixedLengthReader) Read(p []byte) (n int, err error) {
	n, err = r.Reader.Read(p)
	if n > r.length {
		n = r.length
	}
	r.length -= n
	if errors.Is(err, io.EOF) && r.length > 0 {
		err = io.ErrUnexpectedEOF
	}
	if r.length <= 0 {
		err = io.EOF
	}
	return n, err
}

var StopReadingErr = errors.New("stop reading from buffer")

type CallbackReader struct {
	io.Reader
	cb func(int) bool
}

func NewCallbackReader(r io.Reader, cb func(int) bool) *CallbackReader {
	return &CallbackReader{r, cb}
}

func (r *CallbackReader) Read(p []byte) (n int, err error) {
	n, err = r.Reader.Read(p)
	if err != nil {
		return n, err
	}
	if !r.cb(n) {
		return 0, StopReadingErr
	}
	return n, err
}

type PauseableReader struct {
	io.Reader
	isPaused *bool
}

func NewPauseableReader(r io.Reader, isPaused *bool) *PauseableReader {
	return &PauseableReader{r, isPaused}
}

func (r *PauseableReader) Read(p []byte) (n int, err error) {
	if *r.isPaused {
		return 0, nil
	}
	return r.Reader.Read(p)
}

func (r *PauseableReader) SetIsPaused(isPaused *bool) {
	r.isPaused = isPaused
}

type ReaderStack struct {
	pauseable *PauseableReader
	reader    io.Reader
}

func (r *ReaderStack) Read(p []byte) (n int, err error) {
	return r.reader.Read(p)
}

func NewReaderStack(r io.Reader, s *HttpDownloadState) *ReaderStack {
	r = NewFixedLengthReader(r, int(s.left))
	r = NewRateLimitedIO(r, 5*si.Mega)
	r = NewCallbackReader(
		r,
		func(n int) bool {
			s.left -= int64(n)
			s.downloaded += int64(n)
			return s.status != Waiting
		},
	)
	pausable := NewPauseableReader(r, &s.paused)
	return &ReaderStack{pausable, pausable}
}

type Status int

var (
	Waiting   Status = 0
	Started   Status = 1
	Paused    Status = 2
	Completed Status = 2
	Error     Status = 3
)

type HttpDownloadState struct {
	url         string
	status      Status
	err         error
	size        int64
	downloaded  int64
	resumable   bool
	fileName    string
	left        int64
	file        *os.File
	respReader  io.Reader
	readerStack *ReaderStack
	paused      bool
}

func NewHttpDownloadState(url string) *HttpDownloadState {
	return &HttpDownloadState{url: url}
}

func (s *HttpDownloadState) Status() Status {
	return s.status
}

func (s *HttpDownloadState) setError(err error) {
	s.err = err
	if s.err == nil {
		s.status = Completed
	} else {
		s.status = Error
	}
}

func (s *HttpDownloadState) makeRequest(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("invalid status code: %d", resp.StatusCode)
	}
	s.respReader = resp.Body
	return resp, nil
}

func (s *HttpDownloadState) resumeDownloading(req *http.Request) error {
	f, err := os.OpenFile(s.fileName, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		return err
	}
	s.file = f
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-", s.downloaded))
	resp, err := s.makeRequest(req)
	if err != nil {
		return err
	}

	if cr := resp.Header.Get("Content-Range"); cr != "" {
		parts := strings.Split(cr, "/")
		total, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return err
		}
		s.size = total
		parts = strings.Split(parts[0], "-")
		parts = strings.Split(parts[0], " ")

		offset, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return err
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return err
		}
		s.downloaded = offset
	} else {
		return fmt.Errorf("invalid content range")
	}
	s.left = s.size - s.downloaded
	return nil
}

func (s *HttpDownloadState) startDownload(req *http.Request) error {
	resp, err := s.makeRequest(req)
	if err != nil {
		return err
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			s.fileName = params["filename"]
		}
	}
	if s.fileName == "" {
		parsedURL, err := url.Parse(s.url)
		if err != nil {
			return fmt.Errorf("failed to parse url: %s", err)
		}
		s.fileName = path.Base(parsedURL.Path)
	}
	s.size = resp.ContentLength
	s.downloaded = 0
	s.left = s.size
	f, err := os.OpenFile(s.fileName, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		return err
	}
	s.file = f
	return nil
}

func (s *HttpDownloadState) download() error {
	req, err := http.NewRequest("GET", s.url, nil)
	if err != nil {
		return err
	}
	if s.resumable {
		if err := s.resumeDownloading(req); err != nil {
			return err
		}
	} else {
		if err := s.startDownload(req); err != nil {
			return err
		}
	}

	s.readerStack = NewReaderStack(s.respReader, s)
	s.respReader = s.readerStack

	if _, err := io.Copy(s.file, s.respReader); err != nil {
		s.setError(err)
		return err
	} else {
		s.err = nil
		s.status = Completed
	}

	return nil
}

func (s *HttpDownloadState) Start() error {
	if s.status == Paused {
		s.status = Started
		s.paused = false
		return nil
	}
	s.err = nil
	s.status = Started
	go func() {
		defer func() {
			var err error
			switch v := recover().(type) {
			case error:
				err = v
			case nil:
				err = nil
			default:
				err = fmt.Errorf("%v", v)
			}
			s.setError(err)
		}()
		if err := s.download(); err != nil {
			s.setError(err)
		}
	}()
	return nil
}

func (s *HttpDownloadState) Stop() error {
	s.status = Waiting
	return nil
}

func (s *HttpDownloadState) Pause() error {
	s.status = Paused
	s.paused = true
	return nil
}

func (s *HttpDownloadState) Err() error {
	return s.err
}

func (s *HttpDownloadState) GetSize() int64 {
	return s.size
}

func (s *HttpDownloadState) GetDownloaded() int64 {
	return s.downloaded
}

type DownloadState interface {
	Status() Status
	GetSize() int64
	GetDownloaded() int64
	Start() error
	Stop() error
	Pause() error
	Err() error
}

type Downloads struct {
	states []DownloadState
}

func main() {
	state := NewHttpDownloadState("https://releases.ubuntu.com/24.04.3/ubuntu-24.04.3-desktop-amd64.iso?_gl=1*m3ym8z*_gcl_au*NjQ3NTMwNjYxLjE3NTgwMzExNDk.")
	//state.downloaded = 133234688
	//state.resumable = true
	state.Start()
	var lastDownloaded int64
	var hasBeenPaused bool
	for {
		downloaded := state.GetDownloaded()
		if !hasBeenPaused && downloaded >= 100*si.Mega {
			hasBeenPaused = true
			state.Pause()
			go func() {
				time.Sleep(10 * time.Second)
				state.Start()
			}()
		}
		size := state.GetSize()
		fmt.Printf(
			"\r%9s/%s %6.2f%% - %9s/s",
			si.NewBytes(downloaded).String(),
			si.NewBytes(size).String(),
			(float64(downloaded)/float64(size))*100,
			si.NewBytes(downloaded-lastDownloaded).String(),
		)
		lastDownloaded = downloaded
		if state.Status() != Started && state.Status() != Paused {
			break
		}
		time.Sleep(1 * time.Second)
	}
	fmt.Println(state.status, state.Err())
}
