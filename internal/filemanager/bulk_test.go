package filemanager

import (
    "fmt"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestMoveManyPreflightAndNoOverwrite(t *testing.T) {
    manager, root := openTestManager(t)
    if err := manager.Mkdir("", "source"); err != nil { t.Fatal(err) }
    if err := manager.Mkdir("", "target"); err != nil { t.Fatal(err) }
    for _, name := range []string{"first.txt", "second.txt"} {
        if err := manager.Save("source", name, name, true); err != nil { t.Fatal(err) }
    }
    if err := manager.Save("target", "second.txt", "existing", true); err != nil { t.Fatal(err) }
    count, err := manager.MoveMany("source", []string{"first.txt", "second.txt"}, "target")
    if err == nil || count != 0 || !strings.Contains(err.Error(), "already contains") {
        t.Fatalf("collision must reject full batch: count=%d err=%v", count, err)
    }
    if _, err := os.Stat(filepath.Join(root, "source", "first.txt")); err != nil {
        t.Fatalf("first file moved despite conflict: %v", err)
    }
    preserved, err := manager.ReadText("target", "second.txt")
    if err != nil || preserved != "existing" { t.Fatalf("destination overwritten: %q %v", preserved, err) }
    if err := manager.Delete("target", "second.txt"); err != nil { t.Fatal(err) }

    count, err = manager.MoveMany("source", []string{"first.txt", "second.txt"}, "target")
    if err != nil || count != 2 { t.Fatalf("bulk move: count=%d err=%v", count, err) }
    for _, name := range []string{"first.txt", "second.txt"} {
        text, err := manager.ReadText("target", name)
        if err != nil || text != name { t.Fatalf("moved item %q: %q %v", name, text, err) }
    }
}

func TestMoveManyRejectsEscapesDuplicatesAndDescendants(t *testing.T) {
    manager, _ := openTestManager(t)
    if err := manager.Mkdir("", "source"); err != nil { t.Fatal(err) }
    if err := manager.Mkdir("source", "nested"); err != nil { t.Fatal(err) }
    if err := manager.Save("source", "ok.txt", "ok", true); err != nil { t.Fatal(err) }
    for _, names := range [][]string{
        nil, {"../secret"}, {"ok.txt", "ok.txt"}, {".", "ok.txt"},
    } {
        if _, err := manager.MoveMany("source", names, "."); err == nil {
            t.Fatalf("invalid selection accepted: %v", names)
        }
    }
    if _, err := manager.MoveMany("", []string{"source"}, "source/nested"); err == nil {
        t.Fatal("moved directory into its own descendant")
    }
    if _, err := manager.MoveMany("source", []string{"ok.txt"}, "source"); err == nil {
        t.Fatal("accepted same source and destination")
    }
    if _, err := manager.MoveMany("source", []string{"ok.txt"}, "../outside"); err == nil {
        t.Fatal("path traversal accepted")
    }
    text, err := manager.ReadText("source", "ok.txt")
    if err != nil || text != "ok" { t.Fatalf("source changed: %q %v", text, err) }
}

func TestMoveContentsUpFromInsideAndFromParent(t *testing.T) {
    manager, root := openTestManager(t)
    if err := manager.Mkdir("", "public"); err != nil { t.Fatal(err) }
    if err := manager.Mkdir("public", "kirbi.ru"); err != nil { t.Fatal(err) }
    if err := manager.Mkdir("public/kirbi.ru", "vendor"); err != nil { t.Fatal(err) }
    if err := manager.Save("public/kirbi.ru", "index.php", "<?php echo 1;", true); err != nil { t.Fatal(err) }
    if err := manager.Save("public/kirbi.ru/vendor", "autoload.php", "<?php", true); err != nil { t.Fatal(err) }
    count, err := manager.MoveContentsUp("public", "kirbi.ru")
    if err != nil || count != 2 { t.Fatalf("promote from parent: %d %v", count, err) }
    _, err = manager.ReadText("public", "index.php")
    if err != nil { t.Fatalf("file not promoted: %v", err) }
    _, err = manager.ReadText("public/vendor", "autoload.php")
    if err != nil { t.Fatalf("subtree not promoted: %v", err) }
    contents, err := os.ReadDir(filepath.Join(root, "public", "kirbi.ru"))
    if err != nil || len(contents) != 0 { t.Fatalf("source should remain empty: %v %d", err, len(contents)) }

    if err := manager.Mkdir("public", "second"); err != nil { t.Fatal(err) }
    if err := manager.Save("public/second", "readme.txt", "readme", true); err != nil { t.Fatal(err) }
    count, err = manager.MoveContentsUp("public/second", "")
    if err != nil || count != 1 { t.Fatalf("promote from inside: %d %v", count, err) }
    text, err := manager.ReadText("public", "readme.txt")
    if err != nil || text != "readme" { t.Fatalf("promoted readme: %q %v", text, err) }
    if _, err := manager.MoveContentsUp("", ""); err == nil {
        t.Fatal("application root should never be promoted outside its boundary")
    }
}

func TestMoveContentsUpConflictDoesNotPartiallyMove(t *testing.T) {
    manager, root := openTestManager(t)
    if err := manager.Mkdir("", "nested"); err != nil { t.Fatal(err) }
    for _, name := range []string{"aaa.txt", "zzz.txt"} {
        if err := manager.Save("nested", name, "source", true); err != nil { t.Fatal(err) }
    }
    if err := manager.Save("", "zzz.txt", "target", true); err != nil { t.Fatal(err) }
    count, err := manager.MoveContentsUp("nested", "")
    if err == nil || count != 0 { t.Fatalf("expected atomic conflict rejection: %d %v", count, err) }
    if _, err := os.Stat(filepath.Join(root, "nested", "aaa.txt")); err != nil {
        t.Fatalf("first item moved on conflict: %v", err)
    }
    content, err := manager.ReadText("", "zzz.txt")
    if err != nil || content != "target" { t.Fatalf("destination overwritten: %q %v", content, err) }
}

func TestBulkDeleteAndSelectionLimit(t *testing.T) {
    manager, root := openTestManager(t)
    if err := manager.Mkdir("", "items"); err != nil { t.Fatal(err) }
    if err := manager.Save("items", "first", "1", true); err != nil { t.Fatal(err) }
    if err := manager.Save("items", "second", "2", true); err != nil { t.Fatal(err) }
    count, err := manager.DeleteMany("items", []string{"first", "second"})
    if err != nil || count != 2 { t.Fatalf("bulk delete: %d %v", count, err) }
    entries, err := os.ReadDir(filepath.Join(root, "items"))
    if err != nil || len(entries) != 0 { t.Fatalf("bulk delete left files: %v %d", err, len(entries)) }
    many := make([]string, MaxBulkSelection+1)
    for i := range many { many[i] = fmt.Sprintf("file-%d", i) }
    if _, err := manager.DeleteMany("items", many); err == nil {
        t.Fatal("accepted more than the visible-page selection limit")
    }
}

func TestMoveContentsUpNotLimitedByUIPageSize(t *testing.T) {
    manager, root := openTestManager(t)
    if err := manager.Mkdir("", "unpacked"); err != nil { t.Fatal(err) }
    for i := 0; i < MaxDirectoryEntries+5; i++ {
        name := fmt.Sprintf("file-%04d.txt", i)
        if err := manager.Save("unpacked", name, "payload", true); err != nil { t.Fatal(err) }
    }
    count, err := manager.MoveContentsUp("unpacked", "")
    if err != nil || count != MaxDirectoryEntries+5 {
        t.Fatalf("promote beyond page limit: count=%d err=%v", count, err)
    }
    if _, err := os.Stat(filepath.Join(root, "file-0404.txt")); err != nil {
        t.Fatalf("entry outside UI page was not moved: %v", err)
    }
}
