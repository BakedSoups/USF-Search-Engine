package main

import (
	"math"
	"sort"
	"strings"
	"log"
)

func computeIDF(freqByURL map[string]float64, totalDocs int) float64 {
	matchingDocs := len(freqByURL)
	if matchingDocs == 0 {
		return 0
	}
	return math.Log(float64(totalDocs)/float64(matchingDocs + 1)) + 1
}

func search(baseURL string, queryStr string) ([]SearchResult, int) {
    log.Printf("DEBUG: Starting search for query: %s", queryStr)
    
    queryTerms := strings.Fields(strings.ToLower(queryStr))
    if len(queryTerms) == 0 {
        log.Printf("DEBUG: Empty query terms, returning empty results")
        return []SearchResult{}, 0
    }
    
    log.Printf("DEBUG: Query terms: %v", queryTerms)
    
    combinedScores := make(map[string]float64)
    
    if indexDB == nil {
        log.Printf("ERROR: indexDB is nil, search cannot proceed")
        return []SearchResult{}, 0
    }
    
    totalDocs, err := indexDB.GetDocumentCount()
    log.Printf("DEBUG: Total documents in index: %d", totalDocs)
    if err != nil {
        log.Printf("ERROR: Failed to get document count: %v", err)
    }
    
    for _, term := range queryTerms {
        cleanTerm := clean_word(term)
        stemmedTerm := stem_word(cleanTerm)
        
        if stemmedTerm == "" {
            log.Printf("DEBUG: Skipping empty stemmed term for '%s'", term)
            continue
        }
        
        log.Printf("DEBUG: Searching for stemmed term: '%s'", stemmedTerm)
        
        freqByURL, docs, err := indexDB.GetTermFrequencies(stemmedTerm)
        if err != nil {
            log.Printf("ERROR: Search error for term '%s': %v", stemmedTerm, err)
            continue
        }
        
        log.Printf("DEBUG: Found %d documents for term '%s' out of %d total docs", len(freqByURL), stemmedTerm, docs)
        
        if len(freqByURL) == 0 {
            log.Printf("DEBUG: No results found for term '%s'", stemmedTerm)
            continue
        }
        
        idf := computeIDF(freqByURL, docs)
        log.Printf("DEBUG: IDF for term '%s': %f", stemmedTerm, idf)
        
        for docURL, freq := range freqByURL {
            score := freq * idf
            combinedScores[docURL] += score
            log.Printf("DEBUG: URL: %s, Freq: %f, Score: %f", docURL, freq, score)
        }
    }
    
    log.Printf("DEBUG: Combined scores for %d documents", len(combinedScores))
    
    sortedResults := make([]SearchResult, 0, len(combinedScores))
    for url, score := range combinedScores {
        sortedResults = append(sortedResults, SearchResult{
            URL:   url,
            Score: score,
        })
    }
    
    sort.Slice(sortedResults, func(i, j int) bool {
        return sortedResults[i].Score > sortedResults[j].Score
    })
    
    log.Printf("DEBUG: Returning %d sorted results", len(sortedResults))
    
    return sortedResults, len(sortedResults)
}