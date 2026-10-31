package scheduler

import (
 "context"
 "fmt"
 "io"
 "os"
 "path/filepath"
 "strings"

 "github.com/google/uuid"
)

// snapshotCheckout takes a private copy of the checkout before any subsequent Git pull.
// It never follows symlinks or copies Git metadata.
func snapshotCheckout(ctx context.Context, source, root string) (string, error) {
 if err := os.MkdirAll(root, 0o700); err != nil { return "", err }
 target := filepath.Join(root, uuid.NewString())
 if err := os.Mkdir(target, 0o700); err != nil { return "", err }
 err := filepath.WalkDir(source, func(path string, entry os.DirEntry, readErr error) error {
  if readErr != nil { return readErr }
  if err := ctx.Err(); err != nil { return err }
  relative, err := filepath.Rel(source, path)
  if err != nil { return err }
  if relative == "." { return nil }
  if entry.Name() == ".git" && entry.IsDir() { return filepath.SkipDir }
  if entry.Type() & os.ModeSymlink != 0 { return fmt.Errorf("symlink in Git checkout is unsupported: %s", relative) }
  dest := filepath.Join(target, relative)
  if entry.IsDir() { return os.Mkdir(dest, 0o700) }
  if !entry.Type().IsRegular() { return fmt.Errorf("unsupported checkout file: %s", relative) }
  in, err := os.Open(path)
  if err != nil { return err }
  defer in.Close()
  out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
  if err != nil { return err }
  _, copyErr := io.Copy(out, in)
  closeErr := out.Close()
  if copyErr != nil { return copyErr }
  return closeErr
 })
 if err != nil { _ = os.RemoveAll(target); return "", err }
 return target, nil
}

// snapshotPath rewrites an absolute path under the original checkout into the immutable snapshot.
func snapshotPath(path, original, snapshot string) (string, error) {
 if path == "" || !filepath.IsAbs(path) { return path, nil }
 rel, err := filepath.Rel(original, path)
 if err != nil || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
  return "", fmt.Errorf("deployment input references file outside checkout: %s", filepath.Base(path))
 }
 return filepath.Join(snapshot, rel), nil
}
