package service

import (
	"testing"
)

func TestSearchQueries_MetadataFiltering(t *testing.T) {
	idx := &NQEQueryIndex{
		queries: []*NQEQueryIndexEntry{
			{QueryID: "1", Intent: "Show all routes", Description: "Returns all routes in the routing table for each device.", Embedding: []float32{0.1, 0.2}},
			{QueryID: "2", Intent: "", Description: "", Embedding: []float32{0.2, 0.3}},      // Should be ignored
			{QueryID: "3", Intent: "Short", Description: "", Embedding: []float32{0.3, 0.4}}, // Should be ignored
			{QueryID: "4", Intent: "Count routes", Description: "Count the number of routes per device.", Embedding: []float32{0.4, 0.5}},
			{QueryID: "5", Intent: "", Description: "A valid description with enough length.", Embedding: []float32{0.5, 0.6}},
		},
	}

	results, err := idx.SearchQueries("routes", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
	for _, r := range results {
		if r.QueryID == "2" || r.QueryID == "3" {
			t.Errorf("query %s should have been filtered out due to missing/short metadata", r.QueryID)
		}
	}
}

func TestSearchQueries_EmbeddingPreferred(t *testing.T) {
	idx := &NQEQueryIndex{
		queries: []*NQEQueryIndexEntry{
			{QueryID: "1", Intent: "Show all routes", Description: "Returns all routes in the routing table for each device.", Embedding: []float32{0.1, 0.2}},
			{QueryID: "2", Intent: "Show all routes", Description: "Returns all routes in the routing table for each device.", Embedding: nil}, // No embedding
		},
	}

	results, err := idx.SearchQueries("routes", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result with embedding, got %d", len(results))
	}
	if results[0].QueryID != "1" {
		t.Errorf("expected QueryID 1, got %s", results[0].QueryID)
	}
}

func TestSearchQueries_KeywordFallback(t *testing.T) {
	idx := &NQEQueryIndex{
		queries: []*NQEQueryIndexEntry{
			{QueryID: "1", Intent: "Show all routes", Description: "Returns all routes in the routing table for each device.", Embedding: nil},
			{QueryID: "2", Intent: "Count routes", Description: "Count the number of routes per device.", Embedding: nil},
		},
	}

	results, err := idx.SearchQueries("routes", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results from keyword fallback, got %d", len(results))
	}
}
