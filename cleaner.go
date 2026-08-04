package main

import (
	"fmt"
	"log/slog"
	"strings"
	"time"
)

type Cleaner struct {
	cfg      *Config
	jellyfin *JellyfinClient
	sonarr   *SonarrClient
	radarr   *RadarrClient
}

func NewCleaner(cfg *Config, jellyfinKey, sonarrKey, radarrKey string) (*Cleaner, error) {
	return &Cleaner{
		cfg:      cfg,
		jellyfin: NewJellyfinClient(cfg.Jellyfin.URL, jellyfinKey),
		sonarr:   NewSonarrClient(cfg.Sonarr.URL, sonarrKey),
		radarr:   NewRadarrClient(cfg.Radarr.URL, radarrKey),
	}, nil
}

// Run performs one full pass. A failure to resolve the Jellyfin user aborts the
// run (returned error → sooner retry); per-phase failures are logged and do not
// abort the other phase.
func (c *Cleaner) Run() error {
	uid, err := c.jellyfin.UserID(c.cfg.Jellyfin.Username)
	if err != nil {
		return fmt.Errorf("resolve jellyfin user: %w", err)
	}
	if err := c.processMovies(uid); err != nil {
		slog.Error("movie processing failed", "error", err)
	}
	if err := c.processEpisodes(uid); err != nil {
		slog.Error("episode processing failed", "error", err)
	}
	return nil
}

func (c *Cleaner) processMovies(uid string) error {
	items, err := c.jellyfin.WatchedItems(uid, []string{"Movie"})
	if err != nil {
		return fmt.Errorf("jellyfin watched movies: %w", err)
	}
	slog.Info("watched movies", "count", len(items))
	if len(items) == 0 {
		return nil
	}
	cutoff := time.Now().Add(-time.Duration(c.cfg.MovieRetentionDays) * 24 * time.Hour)

	var unmonitorIDs []int64
	type pendingDelete struct {
		id    int64
		title string
	}
	var deletes []pendingDelete

	for _, it := range items {
		tmdb := providerID(it.ProviderIDs, "Tmdb")
		if tmdb == "" {
			slog.Debug("movie has no Tmdb id, skipping", "name", it.Name)
			continue
		}
		m, err := c.radarr.MovieByTMDB(tmdb)
		if err != nil {
			slog.Error("radarr lookup failed", "name", it.Name, "error", err)
			continue
		}
		if m == nil {
			slog.Debug("movie not in Radarr, skipping", "name", it.Name)
			continue
		}
		if c.cfg.UnmonitorOnWatch && m.Monitored {
			unmonitorIDs = append(unmonitorIDs, m.ID)
		}
		if pastRetention(it.UserData, cutoff) {
			deletes = append(deletes, pendingDelete{id: m.ID, title: it.Name})
		}
	}

	if c.cfg.UnmonitorOnWatch && len(unmonitorIDs) > 0 {
		if c.cfg.DryRun {
			slog.Info("[dry-run] would unmonitor movies", "count", len(unmonitorIDs))
		} else if err := c.radarr.UnmonitorMovies(unmonitorIDs); err != nil {
			slog.Error("unmonitor movies failed", "error", err)
		} else {
			slog.Info("unmonitored movies", "count", len(unmonitorIDs))
		}
	}

	for _, d := range deletes {
		if c.cfg.DryRun {
			slog.Info("[dry-run] would delete movie + file and remove from Radarr", "title", d.title)
			continue
		}
		if err := c.radarr.DeleteMovie(d.id); err != nil {
			slog.Error("delete movie failed", "title", d.title, "error", err)
		} else {
			slog.Info("deleted movie (file + Radarr entry)", "title", d.title)
		}
	}
	return nil
}

func (c *Cleaner) processEpisodes(uid string) error {
	items, err := c.jellyfin.WatchedItems(uid, []string{"Episode"})
	if err != nil {
		return fmt.Errorf("jellyfin watched episodes: %w", err)
	}
	slog.Info("watched episodes", "count", len(items))
	if len(items) == 0 {
		return nil
	}
	cutoff := time.Now().Add(-time.Duration(c.cfg.EpisodeRetentionDays) * 24 * time.Hour)

	bySeries := map[string][]jfItem{}
	for _, it := range items {
		if it.SeriesID == "" {
			slog.Debug("episode has no SeriesId, skipping", "name", it.Name)
			continue
		}
		bySeries[it.SeriesID] = append(bySeries[it.SeriesID], it)
	}

	seriesIDs := make([]string, 0, len(bySeries))
	for id := range bySeries {
		seriesIDs = append(seriesIDs, id)
	}
	tvdbMap, err := c.jellyfin.SeriesTVDBIDs(seriesIDs)
	if err != nil {
		return fmt.Errorf("jellyfin series tvdb: %w", err)
	}

	for seriesID, watchedEps := range bySeries {
		tvdb := tvdbMap[seriesID]
		if tvdb == "" {
			slog.Warn("series has no Tvdb id, skipping", "series_id", seriesID)
			continue
		}
		ss, err := c.sonarr.SeriesByTVDB(tvdb)
		if err != nil {
			slog.Error("sonarr series lookup failed", "tvdb", tvdb, "error", err)
			continue
		}
		if ss == nil {
			slog.Debug("series not in Sonarr, skipping", "tvdb", tvdb)
			continue
		}
		sonarrEps, err := c.sonarr.EpisodesBySeries(ss.ID)
		if err != nil {
			slog.Error("sonarr episodes fetch failed", "series", ss.Title, "error", err)
			continue
		}
		idx := make(map[string]sonarrEpisode, len(sonarrEps))
		for _, se := range sonarrEps {
			idx[epKey(se.SeasonNumber, se.EpisodeNumber)] = se
		}

		var unmonitorIDs []int64
		type pendingDelete struct {
			fileID int64
			label  string
		}
		var deletes []pendingDelete

		for _, je := range watchedEps {
			if je.ParentIndexNumber == nil || je.IndexNumber == nil {
				continue
			}
			se, ok := idx[epKey(*je.ParentIndexNumber, *je.IndexNumber)]
			if !ok {
				continue
			}
			if c.cfg.UnmonitorOnWatch && se.Monitored {
				unmonitorIDs = append(unmonitorIDs, se.ID)
			}
			if pastRetention(je.UserData, cutoff) && se.EpisodeFileID != 0 {
				deletes = append(deletes, pendingDelete{
					fileID: se.EpisodeFileID,
					label:  fmt.Sprintf("%s S%02dE%02d", ss.Title, se.SeasonNumber, se.EpisodeNumber),
				})
			}
		}

		if c.cfg.UnmonitorOnWatch && len(unmonitorIDs) > 0 {
			if c.cfg.DryRun {
				slog.Info("[dry-run] would unmonitor episodes", "series", ss.Title, "count", len(unmonitorIDs))
			} else if err := c.sonarr.UnmonitorEpisodes(unmonitorIDs); err != nil {
				slog.Error("unmonitor episodes failed", "series", ss.Title, "error", err)
			} else {
				slog.Info("unmonitored episodes", "series", ss.Title, "count", len(unmonitorIDs))
			}
		}
		for _, d := range deletes {
			if c.cfg.DryRun {
				slog.Info("[dry-run] would delete episode file", "episode", d.label)
				continue
			}
			if err := c.sonarr.DeleteEpisodeFile(d.fileID); err != nil {
				slog.Error("delete episode file failed", "episode", d.label, "error", err)
			} else {
				slog.Info("deleted episode file", "episode", d.label)
			}
		}
	}
	return nil
}

func providerID(ids map[string]string, key string) string {
	if ids == nil {
		return ""
	}
	// ProviderIds keys vary in capitalisation across Jellyfin versions
	// ("Tmdb" vs "TMDB"); match case-insensitively.
	for k, v := range ids {
		if strings.EqualFold(k, key) && v != "" {
			return v
		}
	}
	return ""
}

func epKey(season, episode int) string {
	return fmt.Sprintf("%d-%d", season, episode)
}

// pastRetention reports whether the item was last played before the cutoff.
// Items with no watch date are treated as not eligible (conservative: we won't
// delete something we can't age).
func pastRetention(ud *jfUserData, cutoff time.Time) bool {
	if ud == nil || ud.LastPlayedDate == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, ud.LastPlayedDate)
	if err != nil {
		return false
	}
	return t.Before(cutoff)
}
