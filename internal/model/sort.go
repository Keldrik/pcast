package model

import (
	"sort"
	"time"
)

// SortEpisodesLatest orders episodes for latest output:
// podcast ID ascending, publication time descending (unknown last), episode ID ascending.
func SortEpisodesLatest(episodes []Episode) {
	sort.SliceStable(episodes, func(i, j int) bool {
		a, b := episodes[i], episodes[j]
		if a.PodcastID != b.PodcastID {
			return a.PodcastID < b.PodcastID
		}
		cmp := comparePublishedDescUnknownLast(a.PublishedAt, b.PublishedAt)
		if cmp != 0 {
			return cmp < 0
		}
		return a.ID < b.ID
	})
}

// SortEpisodesCatalog orders episodes for catalog queries:
// publication time descending (unknown last), episode ID descending.
func SortEpisodesCatalog(episodes []Episode) {
	sort.SliceStable(episodes, func(i, j int) bool {
		a, b := episodes[i], episodes[j]
		cmp := comparePublishedDescUnknownLast(a.PublishedAt, b.PublishedAt)
		if cmp != 0 {
			return cmp < 0
		}
		return a.ID > b.ID
	})
}

// SortPodcastsByID orders podcasts by ID ascending.
func SortPodcastsByID(podcasts []Podcast) {
	sort.SliceStable(podcasts, func(i, j int) bool {
		return podcasts[i].ID < podcasts[j].ID
	})
}

// comparePublishedDescUnknownLast returns -1 if a should come before b,
// 1 if after, 0 if equal. Unknown (nil) dates sort last.
func comparePublishedDescUnknownLast(a, b *time.Time) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return 1
	}
	if b == nil {
		return -1
	}
	if a.Equal(*b) {
		return 0
	}
	if a.After(*b) {
		return -1
	}
	return 1
}
