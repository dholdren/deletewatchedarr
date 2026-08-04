package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type RadarrClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func NewRadarrClient(baseURL, apiKey string) *RadarrClient {
	return &RadarrClient{baseURL: baseURL, apiKey: apiKey, http: &http.Client{Timeout: 30 * time.Second}}
}

func (r *RadarrClient) req(method, path string, q url.Values, body any) error {
	u, err := url.Parse(r.baseURL)
	if err != nil {
		return err
	}
	u.Path = path
	if q != nil {
		u.RawQuery = q.Encode()
	}
	// Untyped nil io.Reader when there's no body — see sonarr.go for why.
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, u.String(), bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", r.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("radarr %s %s: %s", method, u.String(), resp.Status)
	}
	return nil
}

type radarrMovie struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Monitored bool   `json:"monitored"`
	HasFile   bool   `json:"hasFile"`
}

// MovieByTMDB returns the Radarr movie for a TMDB id, or nil if not tracked.
func (r *RadarrClient) MovieByTMDB(tmdbID string) (*radarrMovie, error) {
	u, _ := url.Parse(r.baseURL)
	u.Path = "/api/v3/movie"
	u.RawQuery = url.Values{"tmdbId": {tmdbID}}.Encode()
	req, _ := http.NewRequest(http.MethodGet, u.String(), nil)
	req.Header.Set("X-Api-Key", r.apiKey)
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("radarr GET movie: %s", resp.Status)
	}
	var movies []radarrMovie
	if err := json.NewDecoder(resp.Body).Decode(&movies); err != nil {
		return nil, err
	}
	if len(movies) == 0 {
		return nil, nil
	}
	return &movies[0], nil
}

// UnmonitorMovies sets monitored=false for the given movie ids (idempotent).
func (r *RadarrClient) UnmonitorMovies(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	return r.req(http.MethodPut, "/api/v3/movie/editor", nil,
		map[string]any{"movieIds": ids, "monitored": false})
}

// DeleteMovie removes the movie from Radarr and deletes its file from disk.
func (r *RadarrClient) DeleteMovie(id int64) error {
	return r.req(http.MethodDelete, fmt.Sprintf("/api/v3/movie/%d", id),
		url.Values{"deleteFiles": {"true"}}, nil)
}
