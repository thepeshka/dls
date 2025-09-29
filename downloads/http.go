package downloads

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"golang.org/x/time/rate"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
)

var contentRangeRe = regexp.MustCompile(`^bytes (?:(?P<range_start>\d+)?-(?P<range_end>\d+)?|\*)(?:/(?P<size>\d+)|/\*$)?`)

func reGetNamedGroups(r *regexp.Regexp, match []string) map[string]string {
	result := make(map[string]string)
	for i, name := range r.SubexpNames() {
		if i != 0 && name != "" {
			result[name] = match[i]
		}
	}
	return result
}

type parseContentRangeResult struct {
	rangeStart int
	rangeEnd   int
	size       int
}

func parseContentRange(s string) (result parseContentRangeResult, err error) {
	match := contentRangeRe.FindStringSubmatch(s)
	if len(match) == 0 {
		return result, errors.New("invalid content-range format")
	}
	vals := reGetNamedGroups(contentRangeRe, match)
	if rangeStart, ok := vals["range_start"]; ok {
		result.rangeStart, _ = strconv.Atoi(rangeStart)
	}
	if rangeEnd, ok := vals["range_end"]; ok {
		result.rangeEnd, _ = strconv.Atoi(rangeEnd)
	} else {
		result.rangeEnd = -1
	}
	if size, ok := vals["size"]; ok {
		result.size, _ = strconv.Atoi(size)
	} else {
		result.size = -1
	}
	return
}

type SpeedLimiter struct {
	*rate.Limiter
}

func NewSpeedLimiter(bytesPerSec int) *SpeedLimiter {
	var rateLimit rate.Limit
	if bytesPerSec == 0 {
		rateLimit = rate.Inf
	} else {
		rateLimit = rate.Limit(bytesPerSec)
	}
	return &SpeedLimiter{Limiter: rate.NewLimiter(rateLimit, bytesPerSec)}
}

func (l *SpeedLimiter) SetLimit(bytesPerSec int) {
	if bytesPerSec == 0 {
		l.Limiter.SetLimit(rate.Inf)
	} else {
		l.Limiter.SetLimit(rate.Limit(bytesPerSec))
	}
	l.Limiter.SetBurst(bytesPerSec)
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

type RateLimitedIO struct {
	reader  io.Reader
	limiter *SpeedLimiter
}

func NewRateLimitedIO(reader io.Reader, bytesPerSec int) (*RateLimitedIO, *SpeedLimiter) {
	limiter := NewSpeedLimiter(bytesPerSec)
	return &RateLimitedIO{
		reader:  reader,
		limiter: limiter,
	}, limiter
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

type HttpDownloadFile struct {
	Id          uuid.UUID
	Name        string
	Downloaded  int
	Total       int
	Path        string
	URL         string
	err         error
	status      Status
	task        *HttpDownloadTask
	rateLimit   int
	rateLimiter *SpeedLimiter
	resumable   bool
	partSize    int
	file        *os.File
	partial     bool
}

func NewHttpDownloadFile(task *HttpDownloadTask, url string) (*HttpDownloadFile, error) {
	downloadFile := &HttpDownloadFile{
		Id:        uuid.New(),
		Path:      task.Path,
		URL:       url,
		status:    StatusQueued,
		task:      task,
		rateLimit: task.rateLimit,
	}
	resp, err := downloadFile.makeHeadRequest()
	if err != nil {
		return nil, downloadFile.setError(err)
	}
	return downloadFile, downloadFile.parseResponse(resp)
}

func (f *HttpDownloadFile) GetId() uuid.UUID {
	return f.Id
}

func (f *HttpDownloadFile) GetName() string {
	return f.Name
}

func (f *HttpDownloadFile) GetDownloaded() int {
	return f.Downloaded
}

func (f *HttpDownloadFile) GetTotal() int {
	return f.Total
}

func (f *HttpDownloadFile) GetPath() string {
	return f.Path
}

func (f *HttpDownloadFile) setError(err error) error {
	f.err = err
	if err != nil {
		f.status = StatusFailed
	}
	return err
}

func (f *HttpDownloadFile) download(resp *http.Response) error {
	defer resp.Body.Close()
	var r io.Reader
	r = NewFixedLengthReader(resp.Body, f.Total-f.Downloaded)
	r, f.rateLimiter = NewRateLimitedIO(r, f.rateLimit)

	b := make([]byte, 1024)

	for f.status == StatusStarted {
		n, err := r.Read(b)
		if errors.Is(err, io.EOF) {
			f.status = StatusCompleted
			f.task.onFileCompleted(f)
			return nil
		} else if err != nil {
			return err
		}
		if _, err := f.file.Write(b[:n]); err != nil {
			return err
		}
		f.Downloaded += n
	}
	return nil
}

func (f *HttpDownloadFile) setRateLimit(limit int) {
	f.rateLimit = limit
	if f.rateLimiter != nil {
		f.rateLimiter.SetLimit(limit)
	}
}

func (f *HttpDownloadFile) startDownload(resp *http.Response) error {

	defer func() {
		if err := recover(); err != nil {
			f.setError(err.(error))
		}
	}()
	return f.setError(f.download(resp))
}

func (f *HttpDownloadFile) makePartialRequest() (*http.Response, error) {
	req, err := http.NewRequest("GET", f.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-", f.Downloaded))
	return http.DefaultClient.Do(req)
}

func (f *HttpDownloadFile) makeRequest() (*http.Response, error) {
	req, err := http.NewRequest("GET", f.URL, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

func (f *HttpDownloadFile) makeHeadRequest() (*http.Response, error) {
	req, err := http.NewRequest("HEAD", f.URL, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

func (f *HttpDownloadFile) parsePartialResponse(resp *http.Response) error {
	if resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("http download failed with status code %d", resp.StatusCode)
	}
	parseResult, err := parseContentRange(resp.Header.Get("Content-Range"))
	if err != nil {
		return err
	}
	f.Total = parseResult.size
	return nil
}

func (f *HttpDownloadFile) parseResponse(resp *http.Response) error {
	if resp.StatusCode != http.StatusPartialContent {
		return f.parsePartialResponse(resp)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http download failed with status code %d", resp.StatusCode)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			f.Name = params["filename"]
		}
	}
	if f.Path == "" {
		parsedURL, err := url.Parse(f.URL)
		if err != nil {
			return fmt.Errorf("failed to parse url: %s", err)
		}
		f.Name = path.Base(parsedURL.Path)
	}
	if resp.Header.Get("Accept-Ranges") == "bytes" {
		f.resumable = true
	}
	f.Total = int(resp.ContentLength)
	f.Downloaded = 0
	f.partSize = f.Total
	return nil
}

func (f *HttpDownloadFile) makeFile() error {
	file, err := os.OpenFile(f.Path+"/"+f.Name, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		return err
	}
	if _, err := file.Seek(int64(f.Downloaded), io.SeekStart); err != nil {
		return err
	}
	f.file = file
	return nil
}

func (f *HttpDownloadFile) startDownloading() error {
	resp, err := f.makeRequest()
	if err != nil {
		return err
	}
	if err := f.parseResponse(resp); err != nil {
		return err
	}
	if err := f.makeFile(); err != nil {
		return err
	}
	f.status = StatusStarted
	go f.startDownload(resp)
	return nil
}

func (f *HttpDownloadFile) resumeDownloading() error {
	if !f.resumable || f.Downloaded == 0 {
		return f.startDownloading()
	}
	resp, err := f.makePartialRequest()
	if err != nil {
		return err
	}
	if err := f.parseResponse(resp); err != nil {
		return err
	}
	if err := f.makeFile(); err != nil {
		return err
	}
	f.status = StatusStarted
	go f.startDownload(resp)
	return nil
}

type HttpDownloadTask struct {
	Id         uuid.UUID
	Files      []*HttpDownloadFile
	Name       string
	Downloaded int
	Total      int
	Status     Status
	Error      error
	Path       string
	ctx        context.Context
	rateLimit  int
}

func (dt *HttpDownloadTask) GetId() uuid.UUID {
	return dt.Id
}

func (dt *HttpDownloadTask) GetFiles() (files []DownloadFile) {
	for _, file := range dt.Files {
		files = append(files, file)
	}
	return files
}

func (dt *HttpDownloadTask) GetType() DownloadTaskType {
	return DownloadTaskTypeHTTP
}

func (dt *HttpDownloadTask) GetName() string {
	return dt.Name
}

func (dt *HttpDownloadTask) GetDownloaded() int {
	return dt.Downloaded
}

func (dt *HttpDownloadTask) GetTotal() int {
	return dt.Total
}

func (dt *HttpDownloadTask) GetStatus() Status {
	return dt.Status
}

func (dt *HttpDownloadTask) GetError() error {
	return dt.Error
}

func (dt *HttpDownloadTask) GetPath() error {
	return dt.Error
}

func (dt *HttpDownloadTask) Pause() error {
	//TODO implement me
	panic("implement me")
}

func (dt *HttpDownloadTask) _start() (err error) {
	for _, file := range dt.Files {
		if file.status != StatusCompleted {
			if dt.Status == StatusPaused {
				err = file.resumeDownloading()
			} else {
				err = file.startDownloading()
			}
			if err != nil {
				dt.Status = StatusCompleted
				dt.Error = err
			} else {
				dt.Status = StatusStarted
			}
			return
		}
	}
	dt.Status = StatusCompleted
	return nil
}

func (dt *HttpDownloadTask) Start() error {
	if dt.Status != StatusStarted && dt.Status != StatusCompleted {
		return dt._start()
	}
	return nil
}

func (dt *HttpDownloadTask) Stop() error {
	//TODO implement me
	panic("implement me")
}

func (dt *HttpDownloadTask) Delete() error {
	//TODO implement me
	panic("implement me")
}

func (dt *HttpDownloadTask) DeleteWithData() error {
	//TODO implement me
	panic("implement me")
}

func (dt *HttpDownloadTask) onFileCompleted(f *HttpDownloadFile) {
	dt._start()
}
