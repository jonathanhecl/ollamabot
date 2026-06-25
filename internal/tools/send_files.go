package tools

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SendFiles packages one or multiple files/directories and copies them to the session's uploads directory,
// triggering OnAttachmentGenerated.
func SendFiles(workspace, sessionsPath, sessionID string, paths []string, zipName string, handler AttachmentGeneratedHandler) (string, error) {
	if len(paths) == 0 {
		return "", fmt.Errorf("no paths specified")
	}

	var uploadsDir string
	if strings.TrimSpace(sessionsPath) != "" && strings.TrimSpace(sessionID) != "" {
		uploadsDir = filepath.Join(sessionsPath, sessionID, "uploads")
	} else {
		uploadsDir = filepath.Join(workspace, "uploads")
	}

	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create uploads directory: %w", err)
	}

	type pathItem struct {
		resolvedPath string
		relPath      string // relative to workspace root
		isDir        bool
	}
	var items []pathItem

	for _, p := range paths {
		resolved, err := ResolveAndValidatePath(workspace, p)
		if err != nil {
			return "", fmt.Errorf("invalid path %q: %w", p, err)
		}
		stat, err := os.Stat(resolved)
		if err != nil {
			return "", fmt.Errorf("file not found: %s", p)
		}

		rel, err := filepath.Rel(workspace, resolved)
		if err != nil {
			rel = filepath.Base(resolved)
		}

		items = append(items, pathItem{
			resolvedPath: resolved,
			relPath:      rel,
			isDir:        stat.IsDir(),
		})
	}

	// Case 1: Single file, no directory
	if len(items) == 1 && !items[0].isDir {
		filename := filepath.Base(items[0].resolvedPath)
		destPath := filepath.Join(uploadsDir, filename)
		if _, err := os.Stat(destPath); err == nil {
			ext := filepath.Ext(filename)
			noExt := strings.TrimSuffix(filename, ext)
			filename = fmt.Sprintf("%s_%d%s", noExt, time.Now().UnixMilli(), ext)
			destPath = filepath.Join(uploadsDir, filename)
		}

		if err := copyFile(items[0].resolvedPath, destPath); err != nil {
			return "", fmt.Errorf("failed to copy file: %w", err)
		}

		mimeType := detectMimeType(filename)
		if handler != nil && strings.TrimSpace(sessionID) != "" {
			handler.OnAttachmentGenerated(sessionID, filename, mimeType, destPath)
		}
		return fmt.Sprintf("Shared file %q with user.", filename), nil
	}

	// Case 2: Multiple files or directories - Zip them together
	if zipName == "" {
		zipName = "shared_files.zip"
	}
	if !strings.HasSuffix(strings.ToLower(zipName), ".zip") {
		zipName += ".zip"
	}

	destZipName := zipName
	destZipPath := filepath.Join(uploadsDir, destZipName)
	if _, err := os.Stat(destZipPath); err == nil {
		ext := filepath.Ext(destZipName)
		noExt := strings.TrimSuffix(destZipName, ext)
		destZipName = fmt.Sprintf("%s_%d%s", noExt, time.Now().UnixMilli(), ext)
		destZipPath = filepath.Join(uploadsDir, destZipName)
	}

	zipFile, err := os.Create(destZipPath)
	if err != nil {
		return "", fmt.Errorf("failed to create zip archive: %w", err)
	}
	defer zipFile.Close()

	archive := zip.NewWriter(zipFile)
	defer archive.Close()

	for _, item := range items {
		if !item.isDir {
			if err := addFileToZip(archive, item.resolvedPath, item.relPath); err != nil {
				return "", fmt.Errorf("failed to add file %q to archive: %w", item.relPath, err)
			}
		} else {
			err := filepath.Walk(item.resolvedPath, func(walkPath string, info os.FileInfo, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if info.IsDir() {
					return nil
				}
				rel, err := filepath.Rel(workspace, walkPath)
				if err != nil {
					return err
				}
				return addFileToZip(archive, walkPath, rel)
			})
			if err != nil {
				return "", fmt.Errorf("failed to add folder %q to archive: %w", item.relPath, err)
			}
		}
	}

	// Explicitly close writers to flush to disk before calling handler
	if err := archive.Close(); err != nil {
		return "", fmt.Errorf("failed to finalize zip archive: %w", err)
	}
	if err := zipFile.Close(); err != nil {
		return "", fmt.Errorf("failed to close zip file: %w", err)
	}

	if handler != nil && strings.TrimSpace(sessionID) != "" {
		handler.OnAttachmentGenerated(sessionID, destZipName, "application/zip", destZipPath)
	}

	return fmt.Sprintf("Shared zipped files in %q with user.", destZipName), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func addFileToZip(archive *zip.Writer, srcPath, destPath string) error {
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	info, err := srcFile.Stat()
	if err != nil {
		return err
	}

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}

	header.Name = filepath.ToSlash(destPath)
	header.Method = zip.Deflate

	writer, err := archive.CreateHeader(header)
	if err != nil {
		return err
	}

	_, err = io.Copy(writer, srcFile)
	return err
}

func detectMimeType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf":
		return "application/pdf"
	case ".txt", ".md", ".csv", ".log", ".go", ".js", ".ts", ".html", ".css", ".py", ".sh", ".bat", ".ps1", ".json", ".yaml", ".yml", ".xml":
		return "text/plain"
	case ".zip":
		return "application/zip"
	case ".tar", ".gz", ".rar", ".7z":
		return "application/octet-stream"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".mp3", ".wav", ".ogg":
		return "audio/mpeg"
	default:
		return "application/octet-stream"
	}
}
