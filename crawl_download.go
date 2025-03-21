package main

import (
    "io"
    "net/http"
    "database/sql"
	"fmt"
	"sync"
	"time"
	"net/url"
)

// downloads page content from a url
func download(urlStr string) ([]byte, error) {
    allowed, crawlDelay := checkRobotsTxt(urlStr)
    if !allowed {
        return nil, fmt.Errorf("URL disallowed by robots.txt: %s", urlStr)
    }
    
    respectRobotsCrawlDelay(urlStr, crawlDelay)
    
    client := &http.Client{}
    
    req, _ := http.NewRequest("GET", urlStr, nil)
    req.Header.Set("User-Agent", "USF Web Crawler")
    
    resp, err := client.Do(req)
    if err != nil {
        return nil, err
    }
    
    defer resp.Body.Close()
    
    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
    }
    
    return io.ReadAll(resp.Body)
}

func crawlFullyConcurrent(db *sql.DB, seedURL string) (int, error) {
	debugPrint("Starting concurrent crawl of %s", seedURL)
	start := time.Now()

	jobs := make(chan crawlerJob, QUEUE_SIZE)
	results := make(chan crawlerResult, QUEUE_SIZE)
	batches := make(chan documentBatch, MAX_DB_WORKERS)

	debugPrint("Created channels - jobs(cap=%d), results(cap=%d), batches(cap=%d)", 
		QUEUE_SIZE, QUEUE_SIZE, MAX_DB_WORKERS)

    // declares the worker group
	var crawlerWg sync.WaitGroup
	var dbWg sync.WaitGroup

    //init maps for the workers 
	stopMap := create_stop_map()
	visited := make(map[string]bool)
	queued := make(map[string]bool)
	termCache := &sync.Map{}

	debugPrint("Initialized stop word map with %d entries", len(stopMap))

	var mapMutex sync.Mutex
	
	// Create a channel to track active crawlers
	activeCrawlers := make(chan int, MAX_WORKERS)
	
	done := make(chan struct{})

	seedHost, err := url.Parse(seedURL)
	if err != nil {
		return 0, fmt.Errorf("error parsing seed URL: %v", err)
	}

	debugPrint("Starting %d crawler workers", MAX_WORKERS)
	for i := 0; i < MAX_WORKERS; i++ {
		crawlerWg.Add(1)
		go crawlerWorker(i, jobs, results, activeCrawlers, stopMap, &crawlerWg)
	}

	debugPrint("Starting %d database workers", MAX_DB_WORKERS)
	for i := 0; i < MAX_DB_WORKERS; i++ {
		dbWg.Add(1)
		go dbWorker(i, db, batches, &dbWg, termCache)
	}

	// Start the batch collector
	currentBatch := documentBatch{docs: make([]DocumentData, 0, BATCH_SIZE)}
	batchTimer := time.NewTicker(5 * time.Second)
	
	// Function to send current batch
	sendBatch := func() {
		if len(currentBatch.docs)-1 > 0 {
			debugPrint("Sending batch with %d documents to batches channel", len(currentBatch.docs))
			batches <- currentBatch
			currentBatch = documentBatch{docs: make([]DocumentData, 0, BATCH_SIZE)}
		}
	}
	
	//deadlock detector
	go func() {
		deadlockTimer := time.NewTicker(30 * time.Second)
		defer deadlockTimer.Stop()
		
		var lastVisitedCount int
		
		for {
			select {
			case <-deadlockTimer.C:
				mapMutex.Lock()
				currentVisitedCount := len(visited)
				mapMutex.Unlock()
				
				if currentVisitedCount > 0 && currentVisitedCount == lastVisitedCount {
					activeWorkers := len(activeCrawlers)
					debugPrint("DEADLOCK DETECTED! No new pages for 30 seconds. Active workers: %d, Queued: %d, Visited: %d", 
						activeWorkers, len(queued), currentVisitedCount)
					
					debugPrint("Forcing completion...")
					close(done)
					return
				}
				
				lastVisitedCount = currentVisitedCount
				debugPrint("Deadlock detector: %d pages visited, %d active workers", 
					currentVisitedCount, len(activeCrawlers))
				
			case <-done:
				return
			}
		}
	}()
	
	// Start batch collection goroutine
	go func() {
		defer close(batches)
		defer batchTimer.Stop()
		debugPrint("Batch collector started")
		
		//shut down 
		timerDone := make(chan struct{})
		
		//batching 
		go func() {
			timerFlushes := 0
			for {
				select {
				case <-batchTimer.C:
					// Time-based flush
					mapMutex.Lock()
					batchSize := len(currentBatch.docs)
					if batchSize > 0 {
						debugPrint("Timer triggered batch flush with %d documents", batchSize)
						sendBatch()
						timerFlushes++
					}
					mapMutex.Unlock()
				case <-timerDone:
					debugPrint("Batch timer received done signal after %d flushes", timerFlushes)
					return
				}
			}
		}()
		
		docCounter := 0
		batchCounter := 0
		debugPrint("Starting to process crawler results")
		
		completionDetected := false
		
		checkCompletion := func() bool {
			if len(queued) == len(visited) && len(activeCrawlers) == 0 {
				if !completionDetected {
					debugPrint("All queued URLs have been visited (%d) and no active workers, closing jobs channel", len(visited))
					close(jobs)
					completionDetected = true
				}
				return true
			}
			return false
		}
		
	processingLoop:
		for {
			select {
			case result, ok := <-results:
				if !ok {
					debugPrint("Results channel closed")
					break processingLoop
				}
				
				mapMutex.Lock()
				
				visited[result.url] = true
				
				if result.err == nil {
					currentBatch.docs = append(currentBatch.docs, DocumentData{
						URL:       result.url,
						WordCount: result.wordCount,
						WordMap:   result.words,
					})
					
					docCounter++
					debugPrint("Added document %d to current batch: %s", docCounter, result.url)
					debugPrint("links %v ", result.links)
					newLinks := 0
					for _, link := range result.links {
						cleanedURL := clean(result.url, link)
						if visited[cleanedURL] || queued[cleanedURL] {
							continue
						}
						
						linkHost, err := url.Parse(cleanedURL)
						if err != nil {
							continue
						}
						if seedHost.Host == linkHost.Host {
							debugPrint("Queueing new URL: %s", cleanedURL)
							jobs <- crawlerJob{url: cleanedURL}
							queued[cleanedURL] = true
							newLinks++
						}
					}
					debugPrint("Added %d new URLs to jobs channel from %s link count ", newLinks, result.url)
					
					if len(currentBatch.docs) >= BATCH_SIZE {
						debugPrint("Batch size reached (%d), sending batch", BATCH_SIZE)
						sendBatch()
						batchCounter++
					}
				} else {
					debugPrint("Error crawling %s: %v", result.url, result.err)
				}
				
				checkCompletion()
				
				mapMutex.Unlock()
				
			case <-done:
				debugPrint("Received termination signal")
				mapMutex.Lock()
				if !completionDetected {
					debugPrint("Forced termination, closing jobs channel")
					close(jobs)
					completionDetected = true
				}
				mapMutex.Unlock()
				break processingLoop
			}
		}
		
		debugPrint("All crawler results processed, closing timer done channel")
		close(timerDone)
		
		//send all remaining documents
		mapMutex.Lock()
		remainingDocs := len(currentBatch.docs)
		if remainingDocs > 0 {
			debugPrint("Sending final batch with %d documents", remainingDocs)
			sendBatch()
			batchCounter++
		}
		mapMutex.Unlock()
		
		debugPrint("Batch collector finished: processed %d documents in %d batches", 
			docCounter, batchCounter)
	}()
	
    // Where the program starts 

	debugPrint("Adding seed URL to jobs channel: %s", seedURL)
	jobs <- crawlerJob{url: seedURL}
	queued[seedURL] = true
	
	//wait for all crawler workers to be done
	debugPrint("Waiting for crawler workers to finish")
	crawlerWg.Wait()
	debugPrint("All crawler workers finished, closing results channel")
	close(results)
	
	//wait for all the db workers to be done
	debugPrint("Waiting for database workers to finish")
	dbWg.Wait()
	
	totalTime := time.Since(start)
	debugPrint("Crawl completed in %v: processed %d URLs", totalTime, len(visited))
	
	if len(visited) > 0 {
		msPerPage := float64(totalTime.Milliseconds()) / float64(len(visited))
		debugPrint("Performance: %.2f ms/page, %.2f pages/second", 
			msPerPage, 1000.0/msPerPage)
	}
	
	return len(visited), nil
}