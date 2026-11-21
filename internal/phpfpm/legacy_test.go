package phpfpm

import (
    "archive/tar"
    "bytes"
    "compress/gzip"
    "encoding/binary"
    "runtime"
    "strings"
    "testing"
    "debug/elf"
)

func legacyELFFixture() []byte {
    data := make([]byte, 128)
    copy(data, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
    binary.LittleEndian.PutUint16(data[16:18], uint16(elf.ET_DYN))
    machine := elf.EM_X86_64
    if runtime.GOARCH == "arm64" { machine = elf.EM_AARCH64 }
    binary.LittleEndian.PutUint16(data[18:20], uint16(machine))
    binary.LittleEndian.PutUint16(data[52:54], 64)
    binary.LittleEndian.PutUint64(data[40:48], 64) // Section table starts after ELF header.
    binary.LittleEndian.PutUint16(data[58:60], 64) // ELF64 section-header size.
    binary.LittleEndian.PutUint16(data[60:62], 1)  // SHT_NULL section.
    return data
}

func legacyArchiveFixture(t *testing.T, name string, kind byte, body []byte) []byte {
    t.Helper()
    var result bytes.Buffer
    gz := gzip.NewWriter(&result)
    tw := tar.NewWriter(gz)
    header := &tar.Header{Name: name, Size: int64(len(body)), Mode: 0644, Typeflag: kind}
    if kind == tar.TypeSymlink { header.Size = 0; header.Linkname = "../../etc/passwd" }
    if err := tw.WriteHeader(header); err != nil { t.Fatal(err) }
    if kind != tar.TypeSymlink {
        if _, err := tw.Write(body); err != nil { t.Fatal(err) }
    }
    if err := tw.Close(); err != nil { t.Fatal(err) }
    if err := gz.Close(); err != nil { t.Fatal(err) }
    return result.Bytes()
}

func TestExtractLegacyLoader(t *testing.T) {
    if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" { t.Skip("architecture unsupported") }
    elf := legacyELFFixture()
    archive := legacyArchiveFixture(t, "./ioncube/ioncube_loader_lin_7.4.so", tar.TypeReg, elf)
    loaded, err := extractLegacyLoader(bytes.NewReader(archive))
    if err != nil || !bytes.Equal(loaded, elf) {
        t.Fatalf("extract official PHP 7.4 ELF: %v", err)
    }
}

func TestExtractLegacyLoaderRejectsUnsafeArchives(t *testing.T) {
    for name, archive := range map[string][]byte{
        "invalid-gzip": []byte("not a gzip stream"),
        "wrong-version": legacyArchiveFixture(t, "ioncube/ioncube_loader_lin_8.3.so", tar.TypeReg, legacyELFFixture()),
        "symlink": legacyArchiveFixture(t, "ioncube/ioncube_loader_lin_7.4.so", tar.TypeSymlink, nil),
        "garbage": legacyArchiveFixture(t, "ioncube/ioncube_loader_lin_7.4.so", tar.TypeReg, []byte(strings.Repeat("X", 128))),
    } {
        if _, err := extractLegacyLoader(bytes.NewReader(archive)); err == nil {
            t.Errorf("%s was accepted", name)
        }
    }
}

func TestLegacyPHPImageHasRequiredExtensions(t *testing.T) {
    for _, name := range []string{
        "php:7.4-fpm-bullseye", "pdo_mysql", "mysqli", "gd", "zip", "mbstring", "intl", "imagick",
    } {
        if !strings.Contains(legacyDockerfile, name) { t.Errorf("missing %s", name) }
    }
    if strings.Contains(legacyDockerfile, "downloads.ioncube.com") {
        t.Fatal("legacy image must not depend on a direct ionCube download")
    }
}

func TestLegacyContainerName(t *testing.T) {
    if LegacyContainerName(42) != "ogp-php74-42" {
        t.Fatal("wrong deterministic container name")
    }
}
