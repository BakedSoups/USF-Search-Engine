This is a Search Engine all written in go, Crawl a website domain concurrently at the speed of light! 
then search what you want.

## Crawling the USF website,
![image](https://github.com/user-attachments/assets/3d247d05-f057-4b4e-8730-b0f9b6b7d889) ![image](https://github.com/user-attachments/assets/648f692e-9f16-4c94-8002-cf566a5388ed)


The crawler gets a chunk of the information retreived from the website, then uploads the infromation into a sqlite database, 

### Crawling Concurency 
Crawling is purposely set to chunks so it can easily be scalable way to assign 100-1000 workers to work together and conccurently crawl. 

### Way this works 
![image](https://github.com/user-attachments/assets/ce9490a0-5e3a-4f7c-a1fe-59ea30880366)
We have 2 types of workers: 
Extract Worker 
- processes a link and downloads it
- returns the contents and the links found
DB worker 
- gets infromation from extraction
- Uploads this into the database

I then call these paramaters 
```go 
const (
	MAX_WORKERS    = 300  // maximum number of crawler workers
	MAX_DB_WORKERS = 300   // maximum number of database workers
	BATCH_SIZE     = 150  // documents per batch
	QUEUE_SIZE     = 9000 // this is the buffer size of jobs 
						  //determines how many jobs are allowed to be qued
)
```

then this function uses these global paramters to assign the workers the task to work concurently 
``` go 
func crawlFullyConcurrent(db *sql.DB, seedURL string) (int, error)
```

### Scale
with this system of crawling conccreutly this app can scale very well. its just a matter of tuning the paramters for the website. 

## Drawbacks
QUEUE_SIZE is a major bottle neck, if the website its looking at has more than 9000 links the program crashes 
