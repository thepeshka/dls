package downloads

import (
	"github.com/google/uuid"
)

type Status string

const (
	StatusQueued    Status = "queued"
	StatusPaused    Status = "paused"
	StatusStopped   Status = "stopped"
	StatusStarted   Status = "started"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

type DownloadTaskType string

const (
	DownloadTaskTypeHTTP DownloadTaskType = "HTTP"
	DownloadTaskTypeBT   DownloadTaskType = "BT"
)

type DownloadFile interface {
	GetId() uuid.UUID
	GetName() string
	GetDownloaded() int
	GetTotal() int
	GetPath() string
}

type DownloadTask interface {
	GetId() uuid.UUID
	GetFiles() []DownloadFile
	GetType() DownloadTaskType
	GetName() string
	GetDownloaded() int
	GetTotal() int
	GetStatus() Status
	GetError() error
	GetPath() string

	Pause() error
	Start() error
	Stop() error
	Delete() error
	DeleteWithData() error
}
