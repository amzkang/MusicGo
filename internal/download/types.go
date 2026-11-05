package download

import (
	"musicgo/internal/plugin"
	"musicgo/internal/store"
)

type DownloadStatus = store.DownloadStatus

const (
	StatusQueued      = store.DownloadStatusQueued
	StatusDownloading = store.DownloadStatusDownloading
	StatusPaused      = store.DownloadStatusPaused
	StatusCompleted   = store.DownloadStatusCompleted
	StatusFailed      = store.DownloadStatusFailed
	StatusCanceled    = store.DownloadStatusCanceled
)

// Task represents an in-memory active or tracked download task.
// Its state is synchronized by the owning Manager.
type Task struct {
	ID              string           `json:"id"`
	Song            plugin.MusicItem `json:"song"`
	Quality         string           `json:"quality"`
	Status          DownloadStatus   `json:"status"`
	Progress        float64          `json:"progress"` // 0.0 - 1.0
	DownloadedBytes int64            `json:"downloadedBytes"`
	TotalBytes      int64            `json:"totalBytes"`
	Speed           int64            `json:"speed"` // bytes per second
	FilePath        string           `json:"filePath"`
	LyricPath       string           `json:"lyricPath"`
	ErrorMsg        string           `json:"errorMsg"`
	CreatedAt       int64            `json:"createdAt"`
	UpdatedAt       int64            `json:"updatedAt"`
	// Computed from the manager's file cache when a snapshot is taken.
	FileAvailable bool `json:"-"`
}
