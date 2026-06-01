package hoyoverse

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// configININame is the filename Genshin's launcher writes to the game directory.
const configININame = "config.ini"

// utf8BOM is the 3-byte UTF-8 BOM that some Windows tools prepend.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// ReadGameVersion reads the [General].game_version value from
// <gameDir>/config.ini. Errors:
//   - file missing → fmt.Errorf wrapping fs.ErrNotExist
//   - [General] section absent → error
//   - game_version key absent within [General] → error
//
// Tolerates UTF-8 BOM, CRLF / LF line endings, comments (;/# prefixes),
// and lines without `=` (skipped silently).
func ReadGameVersion(gameDir string) (string, error) {
	path := filepath.Join(gameDir, configININame)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	data = bytes.TrimPrefix(data, utf8BOM)

	inGeneral := false
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inGeneral = strings.EqualFold(line, "[General]")
			continue
		}
		if !inGeneral {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if key == "game_version" {
			return val, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan %s: %w", path, err)
	}
	if !inGeneral {
		// We never entered [General] (file may have other sections only).
		return "", fmt.Errorf("config.ini at %s: [General] section not found", gameDir)
	}
	return "", fmt.Errorf("config.ini at %s: game_version key not found in [General]", gameDir)
}

// WriteGameVersion replaces the [General].game_version value in
// <gameDir>/config.ini, preserving all other lines verbatim. The file
// must already exist (read-modify-write semantics; we don't synthesize
// a new config.ini from scratch — risk of clobbering launcher state).
//
// Atomic write: write to <path>.tmp + os.Rename. Line endings are
// preserved from the source (split by LF; if any source line ends with
// '\r', the entire file is written CRLF).
func WriteGameVersion(gameDir, version string) error {
	path := filepath.Join(gameDir, configININame)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	bom := []byte{}
	if bytes.HasPrefix(data, utf8BOM) {
		bom = utf8BOM
		data = data[len(utf8BOM):]
	}

	// Detect line ending convention by sampling first occurrence.
	useCRLF := bytes.Contains(data, []byte("\r\n"))
	lineSep := "\n"
	if useCRLF {
		lineSep = "\r\n"
	}

	// Split, modify, re-join.
	rawLines := bytes.Split(data, []byte("\n"))
	inGeneral := false
	replaced := false
	out := make([][]byte, 0, len(rawLines))
	for _, raw := range rawLines {
		line := raw
		// Strip trailing \r if CRLF; we'll re-add via lineSep.
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		trimmed := strings.TrimSpace(string(line))
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inGeneral = strings.EqualFold(trimmed, "[General]")
			out = append(out, line)
			continue
		}
		if inGeneral && !replaced {
			eq := strings.IndexByte(trimmed, '=')
			if eq > 0 && strings.TrimSpace(trimmed[:eq]) == "game_version" {
				// Preserve original indent (if any) — Genshin's writer doesn't indent
				// but be conservative.
				out = append(out, []byte("game_version="+version))
				replaced = true
				continue
			}
		}
		out = append(out, line)
	}
	if !replaced {
		return fmt.Errorf("config.ini at %s: game_version key not found in [General]; refusing to synthesize", gameDir)
	}

	// Rejoin with detected separator. Preserve trailing newline if source had one
	// (last element of rawLines is empty when source ended with \n).
	body := bytes.Join(out, []byte(lineSep))

	tmpPath := path + ".tmp"
	w := bytes.Buffer{}
	w.Write(bom)
	w.Write(body)
	if err := os.WriteFile(tmpPath, w.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write tmp %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename %s → %s: %w", tmpPath, path, err)
	}
	return nil
}
