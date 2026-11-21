package filemanager

import (
    "bytes"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "errors"
    "io"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestHelperUploadVerifiesDiskContent(t *testing.T) {
    manager, root := openTestManager(t)
    payload := bytes.Repeat([]byte{0, 255, 3, 64, 8, 0, 111, 92}, 32<<10)
    var response bytes.Buffer
    if err := helperUpload(manager, "", "binary.zip", bytes.NewReader(payload), &response); err != nil {
        t.Fatal(err)
    }
    var actual uploadDigest
    if err := json.Unmarshal(response.Bytes(), &actual); err != nil { t.Fatal(err) }
    sum := sha256.Sum256(payload)
    if actual.Size != int64(len(payload)) || actual.SHA256 != hex.EncodeToString(sum[:]) {
        t.Fatalf("disk digest differs from source: %+v", actual)
    }
    saved, err := os.ReadFile(filepath.Join(root, "binary.zip"))
    if err != nil { t.Fatal(err) }
    if !bytes.Equal(payload, saved) { t.Fatal("upload bytes changed during saving") }
}

func TestUploadIntegrityDetectsSizeAndHashMismatch(t *testing.T) {
    source := []byte("Original bytes; central ZIP directory must not change.")
    reader := &digestReader{source: bytes.NewReader(source), digest: sha256.New()}
    if _, err := io.Copy(io.Discard, reader); err != nil { t.Fatal(err) }
    sum := sha256.Sum256(source)
    correct := uploadDigest{Size: int64(len(source)), SHA256: hex.EncodeToString(sum[:])}
    if err := verifyUploadDigest(int64(len(source)), reader, correct); err != nil { t.Fatal(err) }
    if err := verifyUploadDigest(int64(len(source)+1), reader, correct); err == nil || !strings.Contains(err.Error(), "incomplete") {
        t.Fatalf("browser size mismatch not detected: %v", err)
    }
    corrupt := correct
    corrupt.SHA256 = strings.Repeat("0", 64)
    if err := verifyUploadDigest(int64(len(source)), reader, corrupt); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
        t.Fatalf("content corruption not detected: %v", err)
    }
    truncated := correct
    truncated.Size--
    if err := verifyUploadDigest(int64(len(source)), reader, truncated); err == nil || !strings.Contains(err.Error(), "incomplete") {
        t.Fatalf("saved file truncation not detected: %v", err)
    }
}

type failingChecksumWriter struct{}
func (failingChecksumWriter) Write([]byte) (int, error) { return 0, errors.New("test output failure") }

func TestHelperUploadDoesNotLeaveUnverifiedFile(t *testing.T) {
    manager, root := openTestManager(t)
    if err := helperUpload(manager, "", "failed.zip", strings.NewReader("data"), failingChecksumWriter{}); err == nil {
        t.Fatal("expected verification response failure")
    }
    if _, err := os.Stat(filepath.Join(root, "failed.zip")); !errors.Is(err, os.ErrNotExist) {
        t.Fatalf("unverified upload remained on disk: %v", err)
    }

    // A rejected duplicate upload must not delete the existing file.
    if err := manager.Save("", "existing.txt", "keep", true); err != nil { t.Fatal(err) }
    if err := helperUpload(manager, "", "existing.txt", strings.NewReader("replace"), io.Discard); err == nil {
        t.Fatal("duplicate upload was accepted")
    }
    data, err := manager.ReadText("", "existing.txt")
    if err != nil || data != "keep" { t.Fatalf("existing file changed: %q, %v", data, err) }
}
