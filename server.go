package main

import (
  "fmt"
  "time"
  "path"
  "strconv"
  "strings"
  "context"
  "net/http"
  "sync"
  "encoding/base64"
  "encoding/json"
  "crypto/sha512"
)

type kv struct {
	key int
	val string
}

type stat struct {
	ind int
	elapsed time.Duration
}

type writehash struct {
	key int
	resp http.ResponseWriter
	goDie chan int
}

type hashHandler struct {
	sendKv chan kv
	getCounter chan int
	sendInd chan writehash
	sendElapsed chan stat
	getStats chan map[int]time.Duration
	shutdown chan int
}

func hash(ind int, p string, c chan kv, wg *sync.WaitGroup) {
	hashed := sha512.Sum512([]byte(p))
	pass := base64.StdEncoding.EncodeToString(hashed[:])
	time.Sleep(5 * time.Second)
	c <- kv{ind, pass}
	defer wg.Done()
}

func counter(sendCounter chan<- int, getElapsed <-chan stat,
    sendStats chan<- map[int]time.Duration, shutdown <-chan int,
    wg *sync.WaitGroup) {
	counter := 1
	m := make(map[int]time.Duration)
	defer wg.Done()
	for {
		select {
		case sendCounter <- counter:
			counter++
		case statistics := <-getElapsed:
			m[statistics.ind] = statistics.elapsed
		case sendStats <- m:
		case <-shutdown:
			fmt.Println("shutting down counter")
			return
		}
	}
}

func reader(getKv <-chan kv, sendInd <-chan writehash, shutdown <-chan int,
    wg *sync.WaitGroup) {
	m := make(map[int]string)
	defer wg.Done()
	for {
		select {
		case processed := <-getKv:
			m[processed.key] = processed.val
		case ind := <-sendInd:
			val, ok := m[ind.key]
			if ok {
				fmt.Fprintf(ind.resp, "%s", val)
			} else {
				fmt.Fprintf(ind.resp, "not found :(")
			}
			ind.goDie <- ind.key
		case <-shutdown:
			fmt.Println("shutting down reader")
			return
		}
	}
}

func statsReader(h hashHandler) http.HandlerFunc {
  return http.HandlerFunc(func (w http.ResponseWriter, req *http.Request) {
		var total time.Duration = 0
		statistics := <-h.getStats

		length := len(statistics)
		if (length == 0) {
			fmt.Fprintf(w, "nothing to show")
			return
		}

		//calculate average of time spent
		for _,d := range statistics {
			total += d
		}
		average := total / time.Duration(length)
		m := make(map[string]string)
		m["total"] = strconv.Itoa(length)
		m["average"] = average.String()

		js, err := json.Marshal(m)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(js)
  })
}

func shutdownProcessor(h hashHandler) http.HandlerFunc {
  return http.HandlerFunc(func (w http.ResponseWriter, req *http.Request) {
		fmt.Fprintf(w, "bye")
		h.shutdown <- 0
  })
}

func passwdReader(h hashHandler) http.HandlerFunc {
  return http.HandlerFunc(func (w http.ResponseWriter, req *http.Request) {
		diechan := make(chan int)
		ind, err := strconv.Atoi(path.Base(req.URL.Path))
		if err != nil {
			goto err
		}

		h.sendInd <- writehash{ind, w, diechan}
		ind = <-diechan //wait for response to be written to w
		return

err:
		fmt.Fprintf(w, "wrong format")
  })
}

func passwdProcessor(h hashHandler, wg *sync.WaitGroup) http.HandlerFunc {
  return http.HandlerFunc(func (w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		req.ParseForm()
		var i int
		
		if len(req.Form) > 1 {
			goto err
		}

		for k, v := range req.Form {
			if k != "password" {
				goto err
			}

			i = <- h.getCounter
			//kick off hashing thread
			wg.Add(1)
			go hash(i, strings.Join(v, ""), h.sendKv, wg)

			defer func (start time.Time, ind int) {
				elapsed := time.Now().Sub(start)
				h.sendElapsed <- stat{i, elapsed}
			}(start, i)

		}
		fmt.Fprintf(w, "%d", i)
		return

err:		
		fmt.Fprintf(w, "wrong format")
		
  })
}

func main() {
	var wg sync.WaitGroup
	h := hashHandler{
	    make(chan kv),
		make(chan int),
		make(chan writehash),
		make(chan stat),
		make(chan map[int]time.Duration),
		make(chan int),
	}

	mux := http.NewServeMux()

	shutdownCounter := make(chan int)
	shutdownReader := make(chan int)

	/*
	 * DO NOT wg.add for reader as we will do it once we finish
	 * waiting for all hash threads.
	 */
	go reader(h.sendKv, h.sendInd, shutdownReader, &wg)

	wg.Add(1)
	go counter(h.getCounter, h.sendElapsed, h.getStats, shutdownCounter, &wg)

	mux.Handle("/hash", passwdProcessor(h, &wg))
	mux.Handle("/hash/", passwdReader(h))
	mux.Handle("/stats", statsReader(h))
	mux.Handle("/shutdown", shutdownProcessor(h))

	srv := &http.Server{Addr: ":8080", Handler: mux}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			panic(err)
        }
		fmt.Println("server stopped")
    }()
	fmt.Println("server started")

	//wait for shutdown
	<-h.shutdown

	ctx, cancel := context.WithTimeout(context.Background(), 60 * time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		panic(err)
    }

	shutdownCounter <- 0
	wg.Wait() //finish waiting for all hash, counter and server threads
	wg.Add(1)
	shutdownReader <- 0
	wg.Wait() //wait for reader thread to finish
}
  