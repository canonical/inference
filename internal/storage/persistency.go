package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func readDocument(path string) (*Document, error) {
	path = os.ExpandEnv(path)

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		doc := newDocument()
		return &doc, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading storage %s: %w", path, err)
	}
	var doc Document
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing storage %s: %w", path, err)
	}
	return &doc, nil
}

func writeDocument(path string, doc *Document) error {
	path = os.ExpandEnv(path)
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding storage: %w", err)
	}
	data = append(data, '\n')
	if err := replaceFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing storage %s: %w", path, err)
	}
	return nil
}

func lockDocument(path string) (func(), error) {
	path = os.ExpandEnv(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

func readDocumentLocked(path string) (*Document, func(), error) {
	unlock, err := lockDocument(path)
	if err != nil {
		return nil, nil, fmt.Errorf("locking storage: %w", err)
	}
	doc, err := readDocument(path)
	if err != nil {
		unlock()
		return nil, nil, fmt.Errorf("reading storage: %w", err)
	}
	return doc, unlock, nil
}

func replaceFile(path string, data []byte, mode os.FileMode) (err error) {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer func() {
		if removeErr := os.Remove(tempPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("removing temporary storage file: %w", removeErr))
		}
	}()

	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
