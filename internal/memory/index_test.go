package memory

import "testing"

func TestTokenize_Basic(t *testing.T) {
	got := tokenize("Configure Nginx as a Reverse-Proxy, for port 8080!")
	want := []string{"configure", "nginx", "reverse", "proxy", "port", "8080"}
	if len(got) != len(want) {
		t.Fatalf("tokenize: want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tokenize[%d]: want %q, got %q", i, want[i], got[i])
		}
	}
}

func TestTokenize_StopwordsRemoved(t *testing.T) {
	got := tokenize("the quick brown fox is in a hurry")
	for _, tok := range got {
		if stopwords[tok] {
			t.Errorf("tokenize: stopword %q leaked into tokens %v", tok, got)
		}
	}
}

func TestTokenize_Empty(t *testing.T) {
	if got := tokenize(""); len(got) != 0 {
		t.Errorf("tokenize(\"\"): want empty, got %v", got)
	}
	if got := tokenize("   ---   "); len(got) != 0 {
		t.Errorf("tokenize(punctuation only): want empty, got %v", got)
	}
}

func TestInvertedIndex_AddRemoveUpdate(t *testing.T) {
	idx := newInvertedIndex()
	idx.add("doc1", tokenize("nginx reverse proxy"))
	idx.add("doc2", tokenize("postgresql database setup"))

	if idx.docCount != 2 {
		t.Fatalf("docCount: want 2, got %d", idx.docCount)
	}

	scored := idx.score(tokenize("nginx proxy"))
	if len(scored) != 1 || scored[0].key != "doc1" {
		t.Fatalf("score: expected only doc1 to match, got %+v", scored)
	}

	idx.remove("doc1")
	if idx.docCount != 1 {
		t.Fatalf("docCount after remove: want 1, got %d", idx.docCount)
	}
	scored = idx.score(tokenize("nginx proxy"))
	if len(scored) != 0 {
		t.Fatalf("score after remove: expected no matches, got %+v", scored)
	}

	idx.update("doc2", tokenize("postgresql database setup nginx"))
	scored = idx.score(tokenize("nginx"))
	if len(scored) != 1 || scored[0].key != "doc2" {
		t.Fatalf("score after update: expected doc2 to now match nginx, got %+v", scored)
	}
}

func TestInvertedIndex_ScoreEmptyCorpus(t *testing.T) {
	idx := newInvertedIndex()
	if scored := idx.score(tokenize("anything")); scored != nil {
		t.Fatalf("score on empty corpus: want nil, got %+v", scored)
	}
}

func TestInvertedIndex_ScoreNoMatchingTerms(t *testing.T) {
	idx := newInvertedIndex()
	idx.add("doc1", tokenize("nginx reverse proxy"))

	scored := idx.score(tokenize("completely unrelated query"))
	if len(scored) != 0 {
		t.Fatalf("score with no overlapping terms: want empty, got %+v", scored)
	}
}

// TestInvertedIndex_RarerTermWeightedHigher verifies IDF weighting: a query
// term that appears in fewer documents (more distinctive) contributes more
// to a matching document's score than a term appearing in most documents.
func TestInvertedIndex_RarerTermWeightedHigher(t *testing.T) {
	idx := newInvertedIndex()
	// "common" appears in every doc; "rare" appears only in doc1.
	idx.add("doc1", []string{"common", "rare"})
	idx.add("doc2", []string{"common", "other"})
	idx.add("doc3", []string{"common", "other2"})

	scored := idx.score([]string{"common", "rare"})
	var doc1Score, doc2Score float64
	for _, sd := range scored {
		switch sd.key {
		case "doc1":
			doc1Score = sd.score
		case "doc2":
			doc2Score = sd.score
		}
	}
	if doc1Score <= doc2Score {
		t.Errorf("expected doc1 (matches rare term) to outscore doc2 (only matches common term): doc1=%v doc2=%v", doc1Score, doc2Score)
	}
}
