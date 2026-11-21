package output

import (
	"io"
	"net/url"
	"path"
	"strings"

	mapset "github.com/deckarep/golang-set/v2"
	jsoniter "github.com/json-iterator/go"
	"github.com/lc/gau/v2/pkg/providers"
	"github.com/valyala/bytebufferpool"
)

type JSONResult struct {
	Url string `json:"url"`
}

func WriteURLs(writer io.Writer, results <-chan providers.Result, blacklistMap mapset.Set[string], RemoveParameters bool) error {
	lastURL := mapset.NewThreadUnsafeSet[string]()
	for result := range results {
		rawURL := result.URL
		buf := bytebufferpool.Get()
		u, err := url.Parse(rawURL)
		if err != nil {
			continue
		}
		ext := strings.TrimPrefix(strings.ToLower(path.Ext(u.Path)), ".")
		if ext != "" && blacklistMap.Contains(ext) {
			continue
		}

		if RemoveParameters {
			if lastURL.Contains(u.Host + u.Path) {
				continue // already seen this endpoint, skip duplicate params
			}
			lastURL.Add(u.Host + u.Path)
		}

		buf.B = append(buf.B, []byte(rawURL)...)
		buf.B = append(buf.B, "\n"...)
		_, err = writer.Write(buf.B)
		if err != nil {
			return err
		}
		bytebufferpool.Put(buf)
	}
	return nil
}

func WriteURLsJSON(writer io.Writer, results <-chan providers.Result, blacklistMap mapset.Set[string], RemoveParameters bool) {
	var jr JSONResult
	enc := jsoniter.NewEncoder(writer)
	for result := range results {
		rawURL := result.URL
		u, err := url.Parse(rawURL)
		if err != nil {
			continue
		}
		ext := strings.TrimPrefix(strings.ToLower(path.Ext(u.Path)), ".")
		if ext != "" && blacklistMap.Contains(ext) {
			continue
		}
		jr.Url = rawURL
		if err := enc.Encode(jr); err != nil {
			// todo: handle this error
			continue
		}
	}
}
