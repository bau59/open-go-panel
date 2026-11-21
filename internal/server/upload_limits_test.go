package server

import (
    "testing"
    "time"

    "github.com/bau59/open-go-panel/internal/dbmanager"
    "github.com/bau59/open-go-panel/internal/filemanager"
    "github.com/bau59/open-go-panel/internal/uploadlimits"
)

func TestUploadLimitsAreConsistent(t *testing.T) {
    const oneGiB int64 = 1 << 30
    if uploadlimits.FileBytes != oneGiB {
        t.Fatalf("shared file limit = %d, want 1 GiB", uploadlimits.FileBytes)
    }
    if filemanager.MaxUploadBytes != oneGiB || dbmanager.MaxSQLUploadBytes != oneGiB {
        t.Fatalf("app/database limits disagree: app=%d db=%d", filemanager.MaxUploadBytes, dbmanager.MaxSQLUploadBytes)
    }
    if fileUploadBodyLimit < oneGiB+(1<<20) {
        t.Fatalf("HTTP multipart limit %d cannot hold a 1 GiB file plus multipart overhead", fileUploadBodyLimit)
    }
    if filemanager.MaxArchiveBytes != oneGiB {
        t.Fatalf("archive upload limit = %d, want 1 GiB", filemanager.MaxArchiveBytes)
    }
    if filemanager.MaxExtractBytes != uploadlimits.ExpandedBytes || dbmanager.MaxSQLExpandedBytes != uploadlimits.ExpandedBytes {
        t.Fatalf("decompression limits disagree: app=%d db=%d", filemanager.MaxExtractBytes, dbmanager.MaxSQLExpandedBytes)
    }
    if uploadTimeout < 30*time.Minute {
        t.Fatalf("multipart uploads need an extended request deadline: %s", uploadTimeout)
    }
}
