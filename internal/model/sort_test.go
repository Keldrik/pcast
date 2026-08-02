package model

import (
	"testing"
	"time"
)

func TestSortEpisodesLatest(t *testing.T) {
	t1 := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)
	eps := []Episode{
		{ID: 3, PodcastID: 2, PublishedAt: &t1},
		{ID: 1, PodcastID: 1, PublishedAt: nil},
		{ID: 2, PodcastID: 1, PublishedAt: &t2},
		{ID: 4, PodcastID: 1, PublishedAt: &t1},
		{ID: 5, PodcastID: 2, PublishedAt: &t2},
	}
	SortEpisodesLatest(eps)
	want := []int64{2, 4, 1, 5, 3}
	for i, id := range want {
		if eps[i].ID != id {
			t.Fatalf("index %d: got id %d want %d (full=%v)", i, eps[i].ID, id, idsOf(eps))
		}
	}
}

func TestSortEpisodesCatalog(t *testing.T) {
	t1 := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)
	eps := []Episode{
		{ID: 1, PublishedAt: &t1},
		{ID: 2, PublishedAt: &t2},
		{ID: 3, PublishedAt: nil},
		{ID: 4, PublishedAt: &t2},
	}
	SortEpisodesCatalog(eps)
	want := []int64{4, 2, 1, 3}
	for i, id := range want {
		if eps[i].ID != id {
			t.Fatalf("index %d: got id %d want %d (full=%v)", i, eps[i].ID, id, idsOf(eps))
		}
	}
}

func TestSortEpisodesEqualPublishedUsesID(t *testing.T) {
	ts := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	eps := []Episode{
		{ID: 10, PodcastID: 1, PublishedAt: &ts},
		{ID: 5, PodcastID: 1, PublishedAt: &ts},
	}
	SortEpisodesLatest(eps)
	if eps[0].ID != 5 || eps[1].ID != 10 {
		t.Fatalf("latest tie-break: got %v", idsOf(eps))
	}
	SortEpisodesCatalog(eps)
	if eps[0].ID != 10 || eps[1].ID != 5 {
		t.Fatalf("catalog tie-break: got %v", idsOf(eps))
	}
}

func idsOf(eps []Episode) []int64 {
	out := make([]int64, len(eps))
	for i, e := range eps {
		out[i] = e.ID
	}
	return out
}
