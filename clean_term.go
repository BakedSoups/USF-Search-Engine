package main

import (
	"fmt"

	"encoding/json" //JUST for stop words json parsing
	"os"
	"strings"

	"github.com/kljensen/snowball"
)

func clean_word(word string) string {
	word = strings.Trim(word, ".,!?:;'")
	word = strings.ToLower(word)
	return word
}

func stem_word(word string) string {
	stemmed, err := snowball.Stem(word, "engish", true)
	if err != nil {
		return word
	}
	return stemmed
}

// stop map is created in teh begging of the crawl function
func create_stop_map() map[string]bool {
	data, err := os.ReadFile("stop_words.json")
	stop_map := make(map[string]bool)

	if err != nil {
		fmt.Printf("Error reading file: %v\n", err)
		return stop_map
		// when I reach an error should I return a dummy stop map to not break anything?
	}

	var stopWords struct {
		Stopwords []string `json:"stopwords"`
	}

	// converts the json file into go structure
	if err := json.Unmarshal(data, &stopWords); err != nil {
		fmt.Printf("Error parsing JSON: %v\n", err)
		return stop_map
	}

	for i := 0; i < len(stopWords.Stopwords); i++ {
		stop_map[stopWords.Stopwords[i]] = true
	}
	return stop_map
}

func update_word_map(words []string, page_map map[string]int, stop_map map[string]bool) (int, map[string]int) {
	// Add word to map or increment its count
	word_count := 0
	for _, word := range words {

		if stop_map[word] == true {
			continue
		}
		word = clean_word(word)
		word = stem_word(word)
		page_map[word]++
		word_count++
	}

	return word_count, page_map
}
