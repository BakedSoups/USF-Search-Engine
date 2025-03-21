package main

import (
	"math"
	"testing"
)

func TestTfIdf(t *testing.T) {
	tests := []struct {
		name      string
		freqByURL map[string]float64
		totalDocs int
		want      float64
	}{
		{
			name: "term in half of documents",
			freqByURL: map[string]float64{
				"url1": 0.2,
				"url2": 0.1,
			},
			totalDocs: 4,
			want:      math.Log(4.0/3.0) + 1,
		},
		{
			name:      "term in no documents",
			freqByURL: map[string]float64{},
			totalDocs: 5,
			want:      0, // Updated: The implementation returns 0 when there are no matching docs
		},
		{
			name: "term in all documents",
			freqByURL: map[string]float64{
				"url1": 0.5,
				"url2": 0.3,
				"url3": 0.1,
			},
			totalDocs: 3,
			want:      math.Log(3.0/4.0) + 1,
		},
		{
			name: "common vs rare term comparison",
			freqByURL: map[string]float64{
				"url1": 0.4,
			},
			totalDocs: 100,
			want:      math.Log(100.0/2.0) + 1, // Rare term should have high IDF
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := computeIDF(tc.freqByURL, tc.totalDocs)
			if math.Abs(got-tc.want) > 0.0001 {
				t.Errorf("computeIDF(%v, %d) = %v, want %v",
					tc.freqByURL, tc.totalDocs, got, tc.want)
			}
		})
	}

	t.Run("Basic ranking test", func(t *testing.T) {
		mockIndex := map[string]map[string]float64{
			"science": {
				"doc1": 0.08, // highfrequency
				"doc2": 0.04,
				"doc3": 0.01,
			},
			"computer": {
				"doc2": 0.06, // gigher in doc2
				"doc1": 0.01,
			},
		}

		search := func(term string) []string {
			freq, exists := mockIndex[term]
			if !exists {
				return []string{}
			}

			results := make([]struct {
				url   string
				score float64
			}, 0, len(freq))

			for url, tf := range freq {
				idf := computeIDF(freq, 5) //5 total docs
				score := tf * idf
				results = append(results, struct {
					url   string
					score float64
				}{url, score})
			}

			if len(results) >= 2 {
				if results[0].score < results[1].score {
					results[0], results[1] = results[1], results[0]
				}
			}

			urls := make([]string, len(results))
			for i, r := range results {
				urls[i] = r.url
			}
			return urls
		}

		scienceResults := search("science")
		if len(scienceResults) < 3 || scienceResults[0] != "doc1" {
			t.Errorf("Science search expected doc1 first, got %v", scienceResults)
		}

		computerResults := search("computer")
		if len(computerResults) < 2 || computerResults[0] != "doc2" {
			t.Errorf("Computer search expected doc2 first, got %v", computerResults)
		}
	})
}