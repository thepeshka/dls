package main

import (
	"dls/si"
	"errors"
	"fmt"
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

type Status int

var (
	Waiting   Status = 0
	Started   Status = 1
	Paused    Status = 2
	Completed Status = 2
	Error     Status = 3
)

type HttpDownloadState struct {
	url        string
	status     Status
	err        error
	size       int64
	downloaded int64
	resumable  bool
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

func (s *HttpDownloadState) download() error {
	req, err := http.NewRequest("GET", s.url, nil)
	if err != nil {
		return err
	}
	if resumable := s.resumable; resumable {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", s.downloaded))
	} else {
		s.downloaded = 0
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("invalid status code: %d", resp.StatusCode)
	}
	s.resumable = resp.Header.Get("Accept-Ranges") == "bytes"

	var fileName string

	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			fileName = params["filename"]
		}
	}
	if fileName == "" {
		parsedURL, err := url.Parse(s.url)
		if err != nil {
			return fmt.Errorf("failed to parse url: %s", err)
		}
		fileName = path.Base(parsedURL.Path)
	}

	f, err := os.OpenFile(fileName, os.O_WRONLY|os.O_CREATE, 0666)
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
	} else {
		s.size = resp.ContentLength
	}
	left := s.size - s.downloaded

	b := make([]byte, 1024)
	for {
		if s.status == Paused {
			continue
		}
		if s.status == Waiting {
			break
		}
		n, err := resp.Body.Read(b)
		if errors.Is(err, io.EOF) {
			if left > 0 {
				return errors.New("unexpected EOF")
			} else {
				s.err = nil
				s.status = Completed
			}
			return err
		}
		if err != nil {
			return err
		}
		if _, err := f.Write(b[:n]); err != nil {
			return err
		}
		left -= int64(n)
		s.downloaded += int64(n)
	}
	return nil
}

func (s *HttpDownloadState) Start() error {
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
	state.downloaded = 133234688
	state.resumable = true
	state.Start()
	var lastDownloaded int64
	for {
		downloaded := state.GetDownloaded()
		size := state.GetSize()
		fmt.Printf(
			"\r%9s/%s %6.2f%% - %9s/s - %d",
			si.NewBytes(downloaded).String(),
			si.NewBytes(size).String(),
			(float64(downloaded)/float64(size))*100,
			si.NewBytes(downloaded-lastDownloaded).String(),
			state.downloaded,
		)
		lastDownloaded = downloaded
		if state.Status() != Started {
			break
		}
		time.Sleep(1 * time.Second)
	}
	fmt.Println(state.status, state.Err())
}
