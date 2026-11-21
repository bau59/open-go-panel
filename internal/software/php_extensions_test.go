package software

import (
    "archive/tar"
    "bytes"
    "compress/gzip"
    "context"
    "encoding/binary"
    "io"
    "strings"
    "testing"

    "github.com/bau59/open-go-panel/internal/phpfpm"
)

func loaderFixture(t *testing.T, filename string, machine uint16) []byte {
    t.Helper()
    var output bytes.Buffer
    zip := gzip.NewWriter(&output)
    tarWriter := tar.NewWriter(zip)
    elf := make([]byte, 128)
    copy(elf, []byte("\x7fELF"))
    elf[4], elf[5] = 2, 1
    binary.LittleEndian.PutUint16(elf[16:18], 3)
    binary.LittleEndian.PutUint16(elf[18:20], machine)
    hdr := &tar.Header{Name: filename, Mode: 0644, Size: int64(len(elf))}
    if err := tarWriter.WriteHeader(hdr); err != nil { t.Fatal(err) }
    if _, err := tarWriter.Write(elf); err != nil { t.Fatal(err) }
    if err := tarWriter.Close(); err != nil { t.Fatal(err) }
    if err := zip.Close(); err != nil { t.Fatal(err) }
    return output.Bytes()
}

func TestIonCubeDownloadTargetsOfficialHostByArchitecture(t *testing.T) {
    for _, tc := range []struct {
        arch, suffix string
        machine uint16
    }{
        {"amd64", "ioncube_loaders_lin_x86-64.tar.gz", 62},
        {"arm64", "ioncube_loaders_lin_aarch64.tar.gz", 183},
    } {
        url, machine, err := ionCubeURL(tc.arch)
        if err != nil || !strings.HasPrefix(url, "https://downloads.ioncube.com/loader_downloads/") || !strings.HasSuffix(url, tc.suffix) || machine != tc.machine {
            t.Fatalf("%s: url=%s machine=%d err=%v", tc.arch, url, machine, err)
        }
    }
    if _, _, err := ionCubeURL("386"); err == nil { t.Fatal("unsupported architecture accepted") }
}

func TestExtractIonCubePicksMatchingVersionAndValidatesELF(t *testing.T) {
    data := loaderFixture(t, "ioncube/ioncube_loader_lin_8.3.so", 62)
    loader, err := extractIonCube(bytes.NewReader(data), "8.3", 62)
    if err != nil || len(loader) != 128 { t.Fatalf("valid loader: %v, len=%d", err, len(loader)) }
    if _, err := extractIonCube(bytes.NewReader(data), "8.4", 62); err == nil {
        t.Fatal("different PHP version loader accepted")
    }
    if _, err := extractIonCube(bytes.NewReader(data), "8.3", 183); err == nil {
        t.Fatal("different CPU architecture loader accepted")
    }
    if _, err := extractIonCube(bytes.NewReader([]byte("not a tarball")), "8.3", 62); err == nil {
        t.Fatal("broken archive accepted")
    }
}

func TestExtractIonCubeRejectsTarSymlinks(t *testing.T) {
    var buf bytes.Buffer
    gz := gzip.NewWriter(&buf)
    archive := tar.NewWriter(gz)
    err := archive.WriteHeader(&tar.Header{
        Name: "ioncube/ioncube_loader_lin_8.3.so",
        Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd",
    })
    if err != nil { t.Fatal(err) }
    if err := archive.Close(); err != nil { t.Fatal(err) }
    if err := gz.Close(); err != nil { t.Fatal(err) }
    if _, err := extractIonCube(bytes.NewReader(buf.Bytes()), "8.3", 62); err == nil {
        t.Fatal("tar symlink accepted")
    }
}

func TestPHPSoftwareExtensionAllowlist(t *testing.T) {
    for _, name := range []string{"ioncube", "gd", "intl", "bcmath", "redis"} {
        if !allowedPHPExtension(name) { t.Fatalf("missing allowed extension %s", name) }
    }
    for _, name := range []string{"../etc", "ioncube;rm", "foo", "", "ionCube"} {
        if allowedPHPExtension(name) { t.Fatalf("unsafe extension %q was allowed", name) }
        if err := installPHPExtension(context.Background(), phpfpm.Runtime{}, name); err == nil {
            t.Fatalf("unsafe extension %q was installable", name)
        }
    }
}

func TestPHPDownloadDoesNotUseShellOrArbitraryURLs(t *testing.T) {
    // The vendor package is read through archive/tar as data. No shell
    // extraction or arbitrary name is involved in selecting a loader.
    fixture := loaderFixture(t, "./ioncube/ioncube_loader_lin_8.4.so", 62)
    data, err := extractIonCube(bytes.NewReader(fixture), "8.4", 62)
    if err != nil { t.Fatal(err) }
    if _, err := io.Copy(io.Discard, bytes.NewReader(data)); err != nil { t.Fatal(err) }
}
