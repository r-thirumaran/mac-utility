package main

import (
	"encoding/binary"
	"errors"
	"os"
	"strings"
	"unicode/utf16"
)

// lnkInfo is what Reach needs from a Start Menu shortcut: enough to tell
// uninstallers from apps and to find a file to extract an icon from. Parsed
// per MS-SHLLINK; everything is optional and absent fields stay empty.
type lnkInfo struct {
	Target    string // LinkInfo local base path (+ common suffix) or the environment block
	Args      string
	WorkDir   string
	IconPath  string // ICON_LOCATION string (or the icon environment block), %VAR% expanded
	IconIndex int32
}

const (
	lnkHeaderSize = 0x4C

	lnkHasLinkTargetIDList = 1 << 0
	lnkHasLinkInfo         = 1 << 1
	lnkHasName             = 1 << 2
	lnkHasRelativePath     = 1 << 3
	lnkHasWorkingDir       = 1 << 4
	lnkHasArguments        = 1 << 5
	lnkHasIconLocation     = 1 << 6
	lnkIsUnicode           = 1 << 7

	lnkVolumeIDAndLocalBasePath = 1 << 0

	lnkEnvironmentBlock     = 0xA0000001
	lnkIconEnvironmentBlock = 0xA0000007
)

var errBadLnk = errors.New("lnk: not a shell link")

// parseLnk decodes a .lnk file. A truncated or foreign file is an error.
func parseLnk(data []byte) (lnkInfo, error) {
	var info lnkInfo
	if len(data) < lnkHeaderSize || binary.LittleEndian.Uint32(data) != lnkHeaderSize {
		return info, errBadLnk
	}
	flags := binary.LittleEndian.Uint32(data[0x14:])
	info.IconIndex = int32(binary.LittleEndian.Uint32(data[0x38:]))
	pos := lnkHeaderSize
	if flags&lnkHasLinkTargetIDList != 0 {
		if pos+2 > len(data) {
			return info, errBadLnk
		}
		pos += 2 + int(binary.LittleEndian.Uint16(data[pos:]))
	}
	if flags&lnkHasLinkInfo != 0 {
		if pos+4 > len(data) {
			return info, errBadLnk
		}
		size := int(binary.LittleEndian.Uint32(data[pos:]))
		if size < 0x1C || pos+size > len(data) {
			return info, errBadLnk
		}
		info.Target = parseLinkInfo(data[pos : pos+size])
		pos += size
	}
	unicode := flags&lnkIsUnicode != 0
	readString := func() string {
		if pos+2 > len(data) {
			return ""
		}
		n := int(binary.LittleEndian.Uint16(data[pos:]))
		pos += 2
		if unicode {
			if pos+2*n > len(data) {
				return ""
			}
			s := decodeUTF16(data[pos : pos+2*n])
			pos += 2 * n
			return s
		}
		if pos+n > len(data) {
			return ""
		}
		s := string(data[pos : pos+n])
		pos += n
		return s
	}
	if flags&lnkHasName != 0 {
		readString()
	}
	if flags&lnkHasRelativePath != 0 {
		readString()
	}
	if flags&lnkHasWorkingDir != 0 {
		info.WorkDir = readString()
	}
	if flags&lnkHasArguments != 0 {
		info.Args = readString()
	}
	if flags&lnkHasIconLocation != 0 {
		info.IconPath = readString()
	}
	// Extra data blocks: u32 size, u32 signature, payload; a size < 4 ends the list.
	for pos+8 <= len(data) {
		size := int(binary.LittleEndian.Uint32(data[pos:]))
		if size < 8 || pos+size > len(data) {
			break
		}
		sig := binary.LittleEndian.Uint32(data[pos+4:])
		body := data[pos+8 : pos+size]
		switch sig {
		case lnkEnvironmentBlock:
			if info.Target == "" {
				info.Target = envBlockPath(body)
			}
		case lnkIconEnvironmentBlock:
			if info.IconPath == "" {
				info.IconPath = envBlockPath(body)
			}
		}
		pos += size
	}
	info.Target = expandWinEnv(info.Target)
	info.IconPath = expandWinEnv(info.IconPath)
	return info, nil
}

// parseLinkInfo returns LocalBasePath + CommonPathSuffix from a LinkInfo
// structure (offsets are relative to its start).
func parseLinkInfo(li []byte) string {
	headerSize := int(binary.LittleEndian.Uint32(li[4:]))
	flags := binary.LittleEndian.Uint32(li[8:])
	if flags&lnkVolumeIDAndLocalBasePath == 0 {
		return ""
	}
	base := cString(li, int(binary.LittleEndian.Uint32(li[16:])))
	suffix := cString(li, int(binary.LittleEndian.Uint32(li[24:])))
	if headerSize >= 0x24 && len(li) >= 0x24 {
		if off := int(binary.LittleEndian.Uint32(li[28:])); off > 0 {
			base = wString(li, off)
		}
		if off := int(binary.LittleEndian.Uint32(li[32:])); off > 0 {
			suffix = wString(li, off)
		}
	}
	if base == "" {
		return ""
	}
	if suffix != "" && !strings.HasSuffix(base, `\`) {
		return base + `\` + suffix
	}
	return base + suffix
}

// envBlockPath reads an EnvironmentVariableDataBlock payload: 260 ANSI bytes
// then 260 UTF-16 code units, both NUL-terminated.
func envBlockPath(body []byte) string {
	if len(body) >= 260+520 {
		if s := decodeUTF16(body[260 : 260+520]); s != "" {
			return s
		}
	}
	if len(body) >= 260 {
		return cString(body, 0)
	}
	return ""
}

func cString(b []byte, off int) string {
	if off <= 0 || off >= len(b) {
		return ""
	}
	end := off
	for end < len(b) && b[end] != 0 {
		end++
	}
	return string(b[off:end])
}

func wString(b []byte, off int) string {
	if off <= 0 || off+1 >= len(b) {
		return ""
	}
	return decodeUTF16(b[off:])
}

// decodeUTF16 decodes little-endian UTF-16 up to the first NUL.
func decodeUTF16(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		c := binary.LittleEndian.Uint16(b[i:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return string(utf16.Decode(u))
}

// expandWinEnv expands %VAR% references from the environment; unknown
// variables are left as they are.
func expandWinEnv(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var out strings.Builder
	for {
		i := strings.IndexByte(s, '%')
		if i < 0 {
			out.WriteString(s)
			return out.String()
		}
		j := strings.IndexByte(s[i+1:], '%')
		if j < 0 {
			out.WriteString(s)
			return out.String()
		}
		name := s[i+1 : i+1+j]
		out.WriteString(s[:i])
		if v, ok := lookupEnvFold(name); ok {
			out.WriteString(v)
		} else {
			out.WriteString("%" + name + "%")
		}
		s = s[i+1+j+1:]
	}
}

// lookupEnvFold is a case-insensitive os.LookupEnv (Windows semantics).
func lookupEnvFold(name string) (string, bool) {
	if v, ok := os.LookupEnv(name); ok {
		return v, true
	}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}
