package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// robotsData stores robots.txt rules
type robotsData struct {
	allowedPatterns    []*regexp.Regexp
	disallowedPatterns []*regexp.Regexp
	crawlDelay         time.Duration
	lastFetched        time.Time
}

// Global variables for robots data
var (
	robotsInfo     robotsData
	lastDownloaded time.Time
)

// initRobotsData initializes the robots data structure
func initRobotsData() {
	robotsInfo = robotsData{
		allowedPatterns:    make([]*regexp.Regexp, 0),
		disallowedPatterns: make([]*regexp.Regexp, 0),
		crawlDelay:         0,
		lastFetched:        time.Time{},
	}
}

func fetchRobotsTxt(urlStr string) error {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("failed to parse URL: %v", err)
	}

	robotsURL := fmt.Sprintf("%s://%s/robots.txt", parsedURL.Scheme, parsedURL.Host)
	
	resp, err := http.Get(robotsURL)
	if err != nil {
		return fmt.Errorf("failed to fetch robots.txt: %v", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode >= 400 {
		initRobotsData()
		robotsInfo.lastFetched = time.Now()
		return nil
	}
	
	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read robots.txt: %v", err)
	}
	
	initRobotsData()
	
	lines := strings.Split(string(content), "\n")
	userAgentMatches := false
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		
		directive := strings.TrimSpace(strings.ToLower(parts[0]))
		value := strings.TrimSpace(parts[1])
		
		if directive == "user-agent" {
			agent := strings.ToLower(value)
			userAgentMatches = (agent == "*" || strings.Contains(agent, "usf"))
			continue
		}
		
		if !userAgentMatches {
			continue
		}
		
		switch directive {
		case "allow":
			if value != "" {
				pattern := pathToRegex(value)
				re, err := regexp.Compile(pattern)
				if err == nil {
					robotsInfo.allowedPatterns = append(robotsInfo.allowedPatterns, re)
				}
			}
		case "disallow":
			if value != "" {
				pattern := pathToRegex(value)
				re, err := regexp.Compile(pattern)
				if err == nil {
					robotsInfo.disallowedPatterns = append(robotsInfo.disallowedPatterns, re)
				}
			}
		case "crawl-delay":
			delay, err := strconv.ParseFloat(value, 64)
			if err == nil && delay > 0 {
				robotsInfo.crawlDelay = time.Duration(delay * float64(time.Second))
			}
		}
	}
	
	robotsInfo.lastFetched = time.Now()
	return nil
}

// pathToRegex converts a robots.txt path pattern to a regular expression
func pathToRegex(path string) string {
	path = regexp.QuoteMeta(path)
	
	path = strings.ReplaceAll(path, "\\*", ".*")
	path = strings.ReplaceAll(path, "\\$", "$")
	
	return "^" + path
}

// checkRobotsTxt checks if a URL is allowed by robots.txt rules
func checkRobotsTxt(urlStr string) (bool, time.Duration) {
	if robotsInfo.lastFetched.IsZero() || time.Since(robotsInfo.lastFetched) > 24*time.Hour {
		if err := fetchRobotsTxt(urlStr); err != nil {
			return true, 0
		}
	}
	
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return true, robotsInfo.crawlDelay
	}
	
	path := parsedURL.Path
	if path == "" {
		path = "/"
	}
	
	for _, pattern := range robotsInfo.allowedPatterns {
		if pattern.MatchString(path) {
			return true, robotsInfo.crawlDelay
		}
	}
	
	for _, pattern := range robotsInfo.disallowedPatterns {
		if pattern.MatchString(path) {
			return false, robotsInfo.crawlDelay
		}
	}
	
	return true, robotsInfo.crawlDelay
}

func respectRobotsCrawlDelay(urlStr string, delay time.Duration) {
	if delay <= 0 {
		return
	}
	
	elapsed := time.Since(lastDownloaded)
	
	if elapsed < delay {
		time.Sleep(delay - elapsed)
	}
	
	lastDownloaded = time.Now()
}