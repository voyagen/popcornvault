package fetcher

import (
	"bufio"
	"io"
	"regexp"
	"strings"

	"github.com/voyagen/popcornvault/internal/models"
)

var (
	reTvgName   = regexp.MustCompile(`tvg-name="([^"]*)"`)
	reTvgID     = regexp.MustCompile(`tvg-id="([^"]*)"`)
	reTvgLogo   = regexp.MustCompile(`tvg-logo="([^"]*)"`)
	reGroup     = regexp.MustCompile(`group-title="([^"]*)"`)
	reCommaName = regexp.MustCompile(`,([^\n\r\t]*)`)
	reHTTPOrigin   = regexp.MustCompile(`http-origin=(.+)`)
	reHTTPReferrer = regexp.MustCompile(`http-referrer=(.+)`)
	reHTTPUserAgent = regexp.MustCompile(`http-user-agent=(.+)`)
)

// ParseM3U reads an M3U playlist from r and returns channel entries with optional headers.
// useTvgID: if true, prefer tvg-id over comma-alt for channel name when tvg-name is empty.
func ParseM3U(r io.Reader, useTvgID bool) ([]ParsedEntry, error) {
	var entries []ParsedEntry
	scanner := bufio.NewScanner(r)
	// Handle long lines (some M3U have very long EXTINF lines).
	const maxSize = 1024 * 1024
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, maxSize)

	var extinfLine string
	var headers *models.ChannelHttpHeaders
	headersSet := false

	for scanner.Scan() {
		line := scanner.Text()
		lineUpper := strings.ToUpper(line)
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(lineUpper, "#EXTINF"):
			// Previous EXTINF without URL is skipped (malformed)
			extinfLine = line
			headers = nil
			headersSet = false
		case strings.HasPrefix(lineUpper, "#EXTVLCOPT"):
			if headers == nil {
				headers = &models.ChannelHttpHeaders{}
			}
			if s := matchFirst(reHTTPOrigin, line); s != "" {
				headers.HTTPOrigin = &s
				headersSet = true
			}
			if s := matchFirst(reHTTPReferrer, line); s != "" {
				headers.Referrer = &s
				headersSet = true
			}
			if s := matchFirst(reHTTPUserAgent, line); s != "" {
				headers.UserAgent = &s
				headersSet = true
			}
		case trimmed != "":
			// URL line
			if extinfLine == "" {
				continue
			}
			name, err := channelNameFromEXTINF(extinfLine, useTvgID)
			if err != nil {
				extinfLine = ""
				continue
			}
			group := matchFirstPtr(reGroup, extinfLine)
			image := matchFirstPtr(reTvgLogo, extinfLine)
			mediaType := mediaTypeFromURL(trimmed)
			ch := models.Channel{
				Name:      strings.TrimSpace(name),
				URL:       trimmed,
				Group:     group,
				Image:     image,
				MediaType: mediaType,
			}
			var h *models.ChannelHttpHeaders
			if headersSet && headers != nil {
				h = headers
			}
			entries = append(entries, ParsedEntry{Channel: ch, Headers: h})
			extinfLine = ""
			headers = nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func matchFirst(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	v := strings.TrimSpace(m[1])
	if v == "" {
		return ""
	}
	return v
}

func matchFirstPtr(re *regexp.Regexp, s string) *string {
	v := matchFirst(re, s)
	if v == "" {
		return nil
	}
	return &v
}

// channelNameFromEXTINF extracts channel name: tvg-name, or (per useTvgID) tvg-id or comma-alt.
func channelNameFromEXTINF(extinf string, useTvgID bool) (string, error) {
	if n := matchFirst(reTvgName, extinf); n != "" {
		return n, nil
	}
	id := matchFirst(reTvgID, extinf)
	alt := matchFirst(reCommaName, extinf)
	if useTvgID {
		if id != "" {
			return id, nil
		}
		if alt != "" {
			return alt, nil
		}
	} else {
		if alt != "" {
			return alt, nil
		}
		if id != "" {
			return id, nil
		}
	}
	return "", errNoName
}

var errNoName = &parseError{msg: "no name from EXTINF"}

type parseError struct{ msg string }

func (e *parseError) Error() string { return e.msg }

func mediaTypeFromURL(url string) int16 {
	lower := strings.ToLower(url)
	if strings.HasSuffix(lower, ".mp4") || strings.HasSuffix(lower, ".mkv") {
		return models.MediaTypeMovie
	}
	return models.MediaTypeLivestream
}
