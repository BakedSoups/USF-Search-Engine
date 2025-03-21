package main

import (
	"database/sql"
	"fmt"
	"os"
	"sync"
	"time"
	_ "github.com/mattn/go-sqlite3"
)

// configuration constants
const (
	MAX_WORKERS    = 300  // maximum number of crawler workers
	MAX_DB_WORKERS = 300   // maximum number of database workers
	BATCH_SIZE     = 150  // documents per batch
	QUEUE_SIZE     = 9000 // this is the buffer size of jobs 
						  //determines how many jobs are allowed to be qued
)

// debug mode flag - set to false to disable debug output
var DEBUG_MODE = true

// debug print function
func debugPrint(format string, args ...interface{}) {
	if DEBUG_MODE {
		fmt.Printf("[DEBUG] "+format+"\n", args...)
	}
}

// doc data for batches to process
type DocumentData struct {
	URL       string
	WordCount int
	WordMap   map[string]int
}


// these structs help give info to the channels to do differnet task  
// --------------------------------------------------------------------
// URL to process 
type crawlerJob struct {
	url string
}
// output of crawling
type crawlerResult struct {
	url       string
	words     map[string]int
	wordCount int
	links     []string
	err       error
}
// batch of documents for database insertion 
type documentBatch struct {
	docs []DocumentData
}
//---------------------------------------------------------------------


// database-backed inverted index
type InvertedIndexDB struct {
	db          *sql.DB
	initialized bool
	docCount    int
}

// creates a new for inverted index insertion (sets rules)
func NewInvertedIndexDB(dbPath string) (*InvertedIndexDB, error) {
	if _, err := os.Stat(dbPath); err == nil {
		os.Remove(dbPath)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("error opening database: %v", err)
	}
	
	// WAL mode, changes are appended to a separate log file, allowing reads and writes to happen concurrently.
	// sqlite by default only stores 2mb of space in the system memory so we increase it to 1GB 
	// this way frequently visited terms stay in memorys
	_, err = db.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA cache_size = 1000000;
	`)
	if err != nil {
		return nil, fmt.Errorf("error setting PRAGMA options: %v", err)
	}

	idx := &InvertedIndexDB{
		db:          db,
		initialized: false,
		docCount:    0,
	}

	if err := idx.initDatabase(); err != nil {
		db.Close()
		return nil, err
	}

	return idx, nil
}

func (idx *InvertedIndexDB) Close() error {
	if idx.db != nil {
		return idx.db.Close()
	}
	return nil
}

// creates empty tables for the inverted index
func (idx *InvertedIndexDB) initDatabase() error {
	if idx.initialized {
		return nil
	}

	_, err := idx.db.Exec("PRAGMA foreign_keys=OFF;")
	if err != nil {
		return fmt.Errorf("error disabling foreign keys: %v", err)
	}

	tx, err := idx.db.Begin()
	if err != nil {
		return fmt.Errorf("error starting transaction: %v", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	createTermTable := `
	CREATE TABLE TERM (
		ID INTEGER PRIMARY KEY AUTOINCREMENT,
		TERM TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_term_term ON TERM(TERM);
	`

	createLinksTable := `
	CREATE TABLE LINKS (
		ID INTEGER PRIMARY KEY AUTOINCREMENT,
		LINK TEXT NOT NULL,
		TERM_ID INTEGER NOT NULL,
		FREQUENCY REAL NOT NULL,
		FOREIGN KEY(TERM_ID) REFERENCES TERM(ID)
	);
	CREATE INDEX IF NOT EXISTS idx_links_link ON LINKS(LINK);
	CREATE INDEX IF NOT EXISTS idx_links_term_id ON LINKS(TERM_ID);
	`

	createTermKeyTable := `
	CREATE TABLE TERM_KEY (
		ID INTEGER PRIMARY KEY AUTOINCREMENT,
		TERM_ID INTEGER NOT NULL,
		LINK_ID INTEGER NOT NULL,
		FOREIGN KEY(TERM_ID) REFERENCES TERM(ID),
		FOREIGN KEY(LINK_ID) REFERENCES LINKS(ID)
	);
	CREATE INDEX IF NOT EXISTS idx_term_key_term_id ON TERM_KEY(TERM_ID);
	CREATE INDEX IF NOT EXISTS idx_term_key_link_id ON TERM_KEY(LINK_ID);
	`

	createDocumentTable := `
	CREATE TABLE DOCUMENTS (
		ID INTEGER PRIMARY KEY AUTOINCREMENT,
		URL TEXT UNIQUE NOT NULL,
		WORD_COUNT INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_documents_url ON DOCUMENTS(URL);
	`

	_, err = tx.Exec(createTermTable)
	if err != nil {
		return fmt.Errorf("error creating TERM table: %v", err)
	}

	_, err = tx.Exec(createLinksTable)
	if err != nil {
		return fmt.Errorf("error creating LINKS table: %v", err)
	}

	_, err = tx.Exec(createTermKeyTable)
	if err != nil {
		return fmt.Errorf("error creating TERM_KEY table: %v", err)
	}

	_, err = tx.Exec(createDocumentTable)
	if err != nil {
		return fmt.Errorf("error creating DOCUMENTS table: %v", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("error committing transaction: %v", err)
	}

	_, err = idx.db.Exec("PRAGMA foreign_keys=ON;")
	if err != nil {
		return fmt.Errorf("error enabling foreign keys: %v", err)
	}

	idx.initialized = true
	return nil
}

// adds a single document to the index
func (idx *InvertedIndexDB) AddDocument(docURL string, wordCount int, wordCounts map[string]int) error {
	tx, err := idx.db.Begin()
	if err != nil {
		return fmt.Errorf("error starting transaction: %v", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	_, err = tx.Exec("INSERT OR IGNORE INTO DOCUMENTS (URL, WORD_COUNT) VALUES (?, ?)", docURL, wordCount)
	if err != nil {
		return fmt.Errorf("error inserting document: %v", err)
	}

	for term, count := range wordCounts {
		if term == "" {
			continue
		}

		tf := float64(count) / float64(wordCount)

		var termID int64
		err := tx.QueryRow("SELECT ID FROM TERM WHERE TERM = ?", term).Scan(&termID)
		if err != nil {
			if err == sql.ErrNoRows {
				result, err := tx.Exec("INSERT INTO TERM (TERM) VALUES (?)", term)
				if err != nil {
					return fmt.Errorf("error inserting term '%s': %v", term, err)
				}

				termID, err = result.LastInsertId()
				if err != nil {
					return fmt.Errorf("error getting term ID: %v", err)
				}
			} else {
				return fmt.Errorf("error checking for term existence: %v", err)
			}
		}

		result, err := tx.Exec(
			"INSERT INTO LINKS (LINK, TERM_ID, FREQUENCY) VALUES (?, ?, ?)",
			docURL, termID, tf)
		if err != nil {
			return fmt.Errorf("error inserting link: %v", err)
		}

		linkID, err := result.LastInsertId()
		if err != nil {
			return fmt.Errorf("error getting link ID: %v", err)
		}

		_, err = tx.Exec(
			"INSERT INTO TERM_KEY (TERM_ID, LINK_ID) VALUES (?, ?)",
			termID, linkID)
		if err != nil {
			return fmt.Errorf("error inserting term-link relation: %v", err)
		}
	}

	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("error committing transaction: %v", err)
	}

	idx.docCount++
	return nil
}

// returns the total number of documents in the index
func (idx *InvertedIndexDB) GetDocumentCount() (int, error) {
	var count int
	err := idx.db.QueryRow("SELECT COUNT(*) FROM DOCUMENTS").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("error getting document count: %v", err)
	}
	return count, nil
}

// returns term frequencies by url and total document count 
// takes in: term
// outputs: frequency by url map and total documents
func (idx *InvertedIndexDB) GetTermFrequencies(term string) (map[string]float64, int, error) {
	totalDocs, err := idx.GetDocumentCount()
	if err != nil {
		return nil, 0, fmt.Errorf("error getting total document count: %v", err)
	}
	
	if totalDocs == 0 {
		return make(map[string]float64), totalDocs, nil
	}
	
	var termID int
	err = idx.db.QueryRow("SELECT ID FROM TERM WHERE TERM = ?", term).Scan(&termID)
	if err != nil {
		if err == sql.ErrNoRows {
			return make(map[string]float64), totalDocs, nil
		}
		return nil, 0, fmt.Errorf("error finding term: %v", err)
	}
	
	rows, err := idx.db.Query(`
		SELECT LINK, FREQUENCY 
		FROM LINKS 
		WHERE TERM_ID = ?`, termID)
	if err != nil {
		return nil, 0, fmt.Errorf("error getting links for term: %v", err)
	}
	defer rows.Close()
	
	freqByURL := make(map[string]float64)
	for rows.Next() {
		var url string
		var freq float64
		
		if err := rows.Scan(&url, &freq); err != nil {
			return nil, 0, fmt.Errorf("error scanning row: %v", err)
		}
		
		freqByURL[url] = freq
	}
	
	return freqByURL, totalDocs, nil
}
func crawlerWorker(
	id int,
	jobs <-chan crawlerJob,
	results chan<- crawlerResult,
	activeCrawlers chan int,
	stopMap map[string]bool,
	wg *sync.WaitGroup,
) {
	defer wg.Done()
	debugPrint("Crawler worker %d started", id)
	
	jobsProcessed := 0
	for job := range jobs {
		//worker is active signal
		activeCrawlers <- id
		
		debugPrint("Worker %d processing URL: %s", id, job.url)
		
		result := crawlerResult{
			url: job.url,
		}

		body, err := download(job.url)
		if err != nil {
			debugPrint("Worker %d download error: %v", id, err)
			result.err = err
			results <- result
			
			// exit early signal 
			<-activeCrawlers
			continue
		}

		words, links := extract(body)
		
		wordMap := make(map[string]int)
		wordCount, wordMap := update_word_map(words, wordMap, stopMap)
		
		debugPrint("Worker %d processed %s: %d words, %d links", id, job.url, wordCount, len(links))

		result.words = wordMap
		result.wordCount = wordCount
		result.links = links

		debugPrint("Worker %d sending result to channel", id)
		results <- result
		jobsProcessed++
		// this worker is all done 
		<-activeCrawlers
	}
	
	debugPrint("Crawler worker %d finished, processed %d jobs", id, jobsProcessed)
}
// database worker processes batches
func dbWorker(id int, db *sql.DB, batches <-chan documentBatch, wg *sync.WaitGroup, termCache *sync.Map) {
	defer wg.Done()
	debugPrint("DB worker %d started", id)
	
	batchesProcessed := 0
	docsProcessed := 0
	
	for batch := range batches {
		if len(batch.docs) == 0 {
			debugPrint("DB worker %d received empty batch, skipping", id)
			continue
		}

		debugPrint("DB worker %d processing batch of %d documents", id, len(batch.docs))
		batchStart := time.Now()
		
		tx, err := db.Begin()
		if err != nil {
			debugPrint("DB worker %d: error starting transaction: %v", id, err)
			continue
		}

		insertDocStmt, err := tx.Prepare("INSERT OR IGNORE INTO DOCUMENTS (URL, WORD_COUNT) VALUES (?, ?)")
		if err != nil {
			tx.Rollback()
			debugPrint("DB worker %d: error preparing document insert: %v", id, err)
			continue
		}

		insertTermStmt, err := tx.Prepare("INSERT INTO TERM (TERM) VALUES (?)")
		if err != nil {
			insertDocStmt.Close()
			tx.Rollback()
			debugPrint("DB worker %d: error preparing term insert: %v", id, err)
			continue
		}

		insertLinkStmt, err := tx.Prepare("INSERT INTO LINKS (LINK, TERM_ID, FREQUENCY) VALUES (?, ?, ?)")
		if err != nil {
			insertDocStmt.Close()
			insertTermStmt.Close()
			tx.Rollback()
			debugPrint("DB worker %d: error preparing link insert: %v", id, err)
			continue
		}

		insertTermKeyStmt, err := tx.Prepare("INSERT INTO TERM_KEY (TERM_ID, LINK_ID) VALUES (?, ?)")
		if err != nil {
			insertDocStmt.Close()
			insertTermStmt.Close()
			insertLinkStmt.Close()
			tx.Rollback()
			debugPrint("DB worker %d: error preparing term key insert: %v", id, err)
			continue
		}

		// Process all documents in batch
		failed := false
		docsInBatch := len(batch.docs)
		termsProcessed := 0
		
		for _, doc := range batch.docs {
			// Insert document
			_, err = insertDocStmt.Exec(doc.URL, doc.WordCount)
			if err != nil {
				failed = true
				debugPrint("DB worker %d: error inserting document: %v", id, err)
				break
			}

			for term, count := range doc.WordMap {
				if term == "" {
					continue
				}

				termsProcessed++
				tf := float64(count) / float64(doc.WordCount)

				var termID int64
				termIDVal, found := termCache.Load(term)
				if found {
					termID = termIDVal.(int64)
				} else {
					err := tx.QueryRow("SELECT ID FROM TERM WHERE TERM = ?", term).Scan(&termID)
					if err != nil {
						if err == sql.ErrNoRows {
							// term doesn't exist, insert it
							result, err := insertTermStmt.Exec(term)
							if err != nil {
								failed = true
								debugPrint("DB worker %d: error inserting term '%s': %v", id, term, err)
								break
							}

							termID, err = result.LastInsertId()
							if err != nil {
								failed = true
								debugPrint("DB worker %d: error getting term ID: %v", id, err)
								break
							}

							// Cache the term id
							termCache.Store(term, termID)
						} else {
							failed = true
							debugPrint("DB worker %d: error checking for term existence: %v", id, err)
							break
						}
					} else {
						// Cache the found term id
						termCache.Store(term, termID)
					}
				}

				// Insert link with term frequency
				result, err := insertLinkStmt.Exec(doc.URL, termID, tf)
				if err != nil {
					failed = true
					debugPrint("DB worker %d: error inserting link: %v", id, err)
					break
				}

				linkID, err := result.LastInsertId()
				if err != nil {
					failed = true
					debugPrint("DB worker %d: error getting link ID: %v", id, err)
					break
				}

				// Insert term-link relationship
				_, err = insertTermKeyStmt.Exec(termID, linkID)
				if err != nil {
					failed = true
					debugPrint("DB worker %d: error inserting term-link relation: %v", id, err)
					break
				}
			}

			if failed {
				break
			}
		}

		// Clean up prepared statements
		insertDocStmt.Close()
		insertTermStmt.Close()
		insertLinkStmt.Close()
		insertTermKeyStmt.Close()

		// Commit or rollback transaction
		if failed {
			tx.Rollback()
			debugPrint("DB worker %d: rolling back transaction", id)
		} else {
			err = tx.Commit()
			if err != nil {
				debugPrint("DB worker %d: error committing transaction: %v", id, err)
			} else {
				batchesProcessed++
				docsProcessed += docsInBatch
				duration := time.Since(batchStart)
				debugPrint("DB worker %d: processed batch of %d documents (%d terms) in %v", 
					id, docsInBatch, termsProcessed, duration)
			}
		}
	}
	
	debugPrint("DB worker %d finished, processed %d batches with %d documents total", 
		id, batchesProcessed, docsProcessed)
}


// crawls a url and builds the database with concurrent processing
func (idx *InvertedIndexDB) CrawlAndBuildDatabase(seedURL string) (int, error) {
	return crawlFullyConcurrent(idx.db, seedURL)
}
// Inserts the batch directly into the sql-base
func (idx *InvertedIndexDB) ImportInvertedIndex(wordToDocs map[string]map[string]float64) error {
	
	tx, err := idx.db.Begin()
	if err != nil {
		return fmt.Errorf("error starting transaction: %v", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	for term, docs := range wordToDocs {
		if term == "" {
			continue
		}

		var termID int64
		result, err := tx.Exec("INSERT INTO TERM (TERM) VALUES (?)", term)
		if err != nil {
			return fmt.Errorf("error inserting term '%s': %v", term, err)
		}

		termID, err = result.LastInsertId()
		if err != nil {
			return fmt.Errorf("error getting term ID: %v", err)
		}

		for url, freq := range docs {
			var linkID int64
			result, err := tx.Exec(
				"INSERT INTO LINKS (LINK, TERM_ID, FREQUENCY) VALUES (?, ?, ?)",
				url, termID, freq)
			if err != nil {
				return fmt.Errorf("error inserting link: %v", err)
			}

			linkID, err = result.LastInsertId()
			if err != nil {
				return fmt.Errorf("error getting link ID: %v", err)
			}

			_, err = tx.Exec(
				"INSERT INTO TERM_KEY (TERM_ID, LINK_ID) VALUES (?, ?)",
				termID, linkID)
			if err != nil {
				return fmt.Errorf("error inserting term-link relation: %v", err)
			}
			
			_, err = tx.Exec(
				"INSERT OR IGNORE INTO DOCUMENTS (URL, WORD_COUNT) VALUES (?, 0)",
				url)
			if err != nil {
				return fmt.Errorf("error inserting document: %v", err)
			}
		}
	}

	var count int
	err = tx.QueryRow("SELECT COUNT(*) FROM DOCUMENTS").Scan(&count)
	if err != nil {
		return fmt.Errorf("error getting document count: %v", err)
	}
	idx.docCount = count

	return tx.Commit()
}