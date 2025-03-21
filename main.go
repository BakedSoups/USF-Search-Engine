package main

import (
    // "flag"
    "fmt"
    "html/template"
    "log"
    "net/http"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "time"
)

// SearchResult represents a single search result
type SearchResult struct {
    URL       string
    Score     float64
    CleanPath string // New field for clean URL path
}

// SearchTemplateData holds all data needed for the search template
type SearchTemplateData struct {
    Query          string
    Results        []SearchResult
    ResultsCount   int
    SearchTime     int64
    HasResults     bool
    CurrentPage    int
    TotalPages     int
    PagesRange     []int
    ResultsPerPage int
    StartIndex     int
    EndIndex       int
    HasNextPage    bool
    HasPrevPage    bool
}

var (
    indexDB    *InvertedIndexDB
    templates  *template.Template
    baseURL    = "https://www.usfca.edu/"
    
    // Map to store URL to clean path mappings
    urlToPath  = make(map[string]string)
    pathToURL  = make(map[string]string)
)

func main() {
    // Print current working directory for debugging
    wd, err := os.Getwd()
    if err != nil {
        log.Printf("Error getting working directory: %v", err)
    } else {
        log.Printf("Server running from directory: %s", wd)
    }

    // Set up command-line flags
    // nocrawl := flag.Bool("nocrawl", false, "Skip crawling and use existing database")
    // flag.Parse()
   
    indexDB, err = NewInvertedIndexDB("searchSchemas.db")
    if err != nil {
        log.Fatalf("Failed to initialize SQLite database: %v", err)
    }
    defer indexDB.Close()
   
    // Create a FuncMap with our custom functions
    funcMap := template.FuncMap{
        "formatURL": FormatURL,
        "getDisplayTitle": GetDisplayTitle,
        "cleanPath": GetCleanPath,
        "formatFloat": func(f float64) string {
            return fmt.Sprintf("%.4f", f)
        },
        "add": func(a, b int) int {
            return a + b
        },
        "sub": func(a, b int) int {
            return a - b
        },
    }

    // Load template from file - adjusted for your directory structure
    log.Printf("Trying to load template from templates directory")
    
    templatePath := "templates/search_template.html" // First try
    if _, err := os.Stat(templatePath); os.IsNotExist(err) {
        templatePath = "templates/search-template.html"
        if _, err := os.Stat(templatePath); os.IsNotExist(err) {
            log.Printf("Could not find template at %s, checking working directory", templatePath)
            
            templatePath = "search_template.html"
            if _, err := os.Stat(templatePath); os.IsNotExist(err) {
                log.Fatalf("Template file not found in templates directory or current directory")
            }
        }
    }
    
    log.Printf("Found template at: %s", templatePath)
    
    templates, err = template.New(filepath.Base(templatePath)).Funcs(funcMap).ParseFiles(templatePath)
    if err != nil {
        log.Fatalf("Failed to parse template: %v", err)
    }
    
    log.Printf("Available templates:")
    for _, t := range templates.Templates() {
        log.Printf("- %s", t.Name())
    }
    
    http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("templates"))))
    
    // Add search handlers
    http.HandleFunc("/search", handleSearch)
    http.HandleFunc("/top10/search", handleSearch) // For backward compatibility
    
    // Add result detail handler
    http.HandleFunc("/search/result/", handleResultDetail)
    
    
    // Pre-crawl to build database (improves first search experience)
    go func() {
        fmt.Println("Pre-crawling content to build database...")
        docCount, err := indexDB.CrawlAndBuildDatabase(baseURL)
        if err != nil {
            log.Printf("Warning: Failed to pre-crawl content: %v", err)
        } else {
            fmt.Printf("Pre-crawl complete: indexed %d documents\n", docCount)
        }
        
    }()
    fmt.Println("Starting server at http://localhost:8080/top10/search")
    if err := http.ListenAndServe(":8080", nil); err != nil {
        fmt.Printf("Error starting server: %v\n", err)
    }
    
}

// GetCleanPath generates a clean URL-friendly path for a URL
func GetCleanPath(urlStr string) string {
    // Check if we already have a clean path for this URL
    if path, exists := urlToPath[urlStr]; exists {
        return path
    }
    
    title := GetDisplayTitle(urlStr)
    
    // Convert the title to a slug
    slug := strings.ToLower(title)
    slug = strings.ReplaceAll(slug, " ", "-")
    slug = strings.ReplaceAll(slug, "/", "-")
    slug = strings.ReplaceAll(slug, ".", "-")
    
    // Add some randomness to avoid collisions
    timestamp := time.Now().UnixNano() % 1000
    slug = fmt.Sprintf("%s-%d", slug, timestamp)
    
    // Store the mapping
    urlToPath[urlStr] = slug
    pathToURL[slug] = urlStr
    
    return slug
}

func FormatURL(urlStr string) string {
    return strings.TrimPrefix(urlStr, "https://")
}

// GetDisplayTitle extracts a title from URL
func GetDisplayTitle(urlStr string) string {
    parts := strings.Split(urlStr, "/")

    for i := len(parts) - 1; i >= 0; i-- {
        part := parts[i]
        if part != "" {
            part = strings.TrimSuffix(part, ".html")
            part = strings.ReplaceAll(part, "-", " ")
            part = strings.ReplaceAll(part, "_", " ")
            
            words := strings.Fields(part)
            for j, word := range words {
                if len(word) > 0 {
                    words[j] = strings.ToUpper(word[0:1]) + strings.ToLower(word[1:])
                }
            }
            
            result := strings.Join(words, " ")
            if result != "" {
                return result
            }
        }
    }
    
    // when the user clicks it go backs to the domain
    return strings.TrimPrefix(urlStr, "https://")
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
    log.Printf("Handling search request: %s", r.URL.String())
    
    // get the search parameters
    query := r.URL.Query().Get("q")
    pageStr := r.URL.Query().Get("page")
    
    // set up the template data 
    data := SearchTemplateData{
        Query: query,
        ResultsPerPage: 10,
        CurrentPage: 1,
    }
    
    // Parse page number
    if pageStr != "" {
        page, err := strconv.Atoi(pageStr)
        if err == nil && page > 0 {
            data.CurrentPage = page
        }
    }
    
    //pagination indexes
    data.StartIndex = (data.CurrentPage - 1) * data.ResultsPerPage
    
    // Perform search if query exists
    if query != "" {
        startTime := time.Now()
        
        //gather search results from inverted index
        searchResults, totalCount := search(baseURL, query)
        
        results := make([]SearchResult, len(searchResults))
        for i, result := range searchResults {
            results[i] = SearchResult{
                URL:       result.URL,
                Score:     result.Score,
                CleanPath: GetCleanPath(result.URL),
            }
        }
        
        data.Results = results
        data.ResultsCount = totalCount
        data.HasResults = totalCount > 0
        data.SearchTime = time.Since(startTime).Milliseconds()
        
        // calclate the pagination info
        data.TotalPages = (totalCount + data.ResultsPerPage - 1) / data.ResultsPerPage
        endIndex := data.StartIndex + data.ResultsPerPage
        if endIndex > totalCount {
            endIndex = totalCount
        }
        data.EndIndex = endIndex
        data.HasNextPage = data.CurrentPage < data.TotalPages
        data.HasPrevPage = data.CurrentPage > 1
        
        // Generate page range for pagination
        if data.TotalPages > 1 {
            start := max(1, data.CurrentPage-2)
            end := min(data.TotalPages, start+4)
            
            // Adjust start if we have too few pages at the end
            if end-start < 4 {
                start = max(1, end-4)
            }
            
            data.PagesRange = make([]int, 0, end-start+1)
            for i := start; i <= end; i++ {
                data.PagesRange = append(data.PagesRange, i)
            }
        }
        
        // Slice results for current page
        if len(results) > 0 && data.StartIndex < len(results) {
            endIdx := min(endIndex, len(results))
            data.Results = results[data.StartIndex:endIdx]
        }
    }
    
    // Use correct template name
    templateName := filepath.Base(templates.Name())
    log.Printf("Executing template '%s'", templateName)
    
    err := templates.ExecuteTemplate(w, templateName, data)
    if err != nil {
        log.Printf("Error executing template: %v", err)
        http.Error(w, fmt.Sprintf("Error executing template: %v", err), http.StatusInternalServerError)
    }
}

// handleResultDetail processes clean URLs and redirects to the original URL
func handleResultDetail(w http.ResponseWriter, r *http.Request) {
    parts := strings.Split(r.URL.Path, "/")
    if len(parts) < 3 {
        http.NotFound(w, r)
        return
    }
    
    slug := parts[len(parts)-1]
    log.Printf("Handling result detail for slug: %s", slug)
    
    originalURL, exists := pathToURL[slug]
    if !exists {
        log.Printf("URL not found for slug: %s", slug)
        http.NotFound(w, r)
        return
    }
    
    log.Printf("Redirecting to original URL: %s", originalURL)
    
    http.Redirect(w, r, originalURL, http.StatusFound)
}

func min(a, b int) int {
    if a < b {
        return a
    }
    return b
}

func max(a, b int) int {
    if a > b {
        return a
    }
    return b
}