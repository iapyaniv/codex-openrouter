package distribution

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

const noticePrefix = "internal/distribution/licenses/"

// Voice notices remain inside the pinned native inventory.
//
//go:embed licenses
var noticesFS embed.FS

// Notice records must match the embedded bytes before any launch is possible.
func NoticeNames() []string {
	records := cachedManifest.Notices.noticeRecords()
	names := make([]string, 0, len(records))
	for _, record := range records {
		names = append(names, path.Base(record.Path))
	}
	return names
}

// Notice returns the exact embedded bytes for an installed notice name.
func Notice(name string) ([]byte, error) {
	if !isCleanRelative(name) || strings.ContainsRune(name, '/') {
		return nil, fs.ErrNotExist
	}
	data, err := noticesFS.ReadFile("licenses/" + name)
	if err != nil {
		return nil, err
	}
	return bytes.Clone(data), nil
}

func (notices manifestNotices) noticeRecords() []noticeRecord {
	var records []noticeRecord
	if notices.WrapperNotice != nil {
		records = append(records, *notices.WrapperNotice)
	}
	records = append(records, notices.CodexRootNotices...)
	records = append(records, notices.BundledNotices...)
	return records
}

func (notices manifestNotices) validate() error {
	records := notices.noticeRecords()
	if len(records) == 0 {
		return errors.New("no retained notices")
	}
	seen := make(map[string]bool)
	for _, record := range records {
		name, ok := strings.CutPrefix(record.Path, noticePrefix)
		if !ok || !isCleanRelative(name) || strings.ContainsRune(name, '/') {
			return fmt.Errorf("notice path %q is not inside %s", record.Path, noticePrefix)
		}
		if seen[name] {
			return fmt.Errorf("duplicate notice %q", name)
		}
		seen[name] = true
		data, err := noticesFS.ReadFile("licenses/" + name)
		if err != nil {
			return fmt.Errorf("notice %q is not embedded: %v", record.Path, err)
		}
		if int64(len(data)) != record.Bytes || sha256Of(data) != record.SHA256 {
			return fmt.Errorf("notice %q bytes or digest do not match the manifest", record.Path)
		}
	}
	return nil
}
