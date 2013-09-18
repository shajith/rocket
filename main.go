package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

// Hop-by-hop headers. These are removed when sent to the backend.
// http://www.w3.org/Protocols/rfc2616/rfc2616-sec13.html
var hopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te", // canonicalized version of "TE"
	"Trailers",
	"Transfer-Encoding",
	"Upgrade",
}

func fetch(urlStr string, req *http.Request, c chan string) {
	host := config["host"]
	transport := http.DefaultTransport

	outreq := new(http.Request)

	outreq.Proto = "HTTP/1.1"
	outreq.ProtoMajor = 1
	outreq.ProtoMinor = 1
	outreq.Close = false
	outreq.URL, _ = url.Parse(strings.Join([]string{host, urlStr}, ""))

	outreq.Header = make(http.Header)
	copyHeader(outreq.Header, req.Header)

	for _, h := range hopHeaders {
		if outreq.Header.Get(h) != "" {
			outreq.Header.Del(h)
		}
	}

	if clientIP, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
		// If we aren't the first proxy retain prior
		// X-Forwarded-For information as a comma+space
		// separated list and fold multiple headers into one.
		if prior, ok := outreq.Header["X-Forwarded-For"]; ok {
			clientIP = strings.Join(prior, ", ") + ", " + clientIP
		}
		outreq.Header.Set("X-Forwarded-For", clientIP)
	}

	outreq.Header.Set("Accept", "application/json")

	res, err := transport.RoundTrip(outreq)

	if err != nil {
		log.Printf("http: proxy error: %v", err)
		c <- "error"
		return
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		c <- "error"
		return
	}

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		fmt.Printf("Error")
	}

	fmt.Println("Fetched: ", urlStr)

	c <- fmt.Sprintf("{\"url\":\"%s\",\"data\":%s}", urlStr, string(body))
}

func bundleNameFromPath(path string) string {
	parts := strings.Split(path, "/")
	last := parts[len(parts)-1]
	return strings.Split(last, ".")[0]
}

func handler(w http.ResponseWriter, r *http.Request) {
	bundleName := bundleNameFromPath(r.URL.Path)

	var urls []string

	if bundles[bundleName] != nil {
		urls = bundles[bundleName]
	} else {
		urls = bundles["bootstrap"]
	}

	c := make(chan string)

	for _, url := range urls {
		go fetch(url, r, c)
	}

	header := w.Header()
	header.Set("Content-Type", "application/json")

	fmt.Fprintf(w, "{\"responses\":[")

	num := len(urls)

	for i := 0; i < num; i++ {
		str := <-c
		if str != "error" {
			if( i == num - 1) {
				fmt.Fprintf(w, "%s", str)
			} else {
				fmt.Fprintf(w, "%s,", str)
			}
		}
	}

	fmt.Fprintf(w, "]}")
}

var bundles map[string][]string
var config map[string]string
var host string

func readConfig(path string) []byte {
	file, err := os.Open(path)
	if err != nil {
		panic(fmt.Sprintf("Error reading %s", path))
	}

	defer file.Close()

	data, err := ioutil.ReadAll(file)
	if err != nil {
		panic(fmt.Sprintf("Error reading %s", path))
	}

	return data
}

func main() {

	json.Unmarshal(readConfig("bundles.json"), &bundles)
	json.Unmarshal(readConfig("config.json"), &config)

	var port string
	http.HandleFunc("/", handler)
	if config["port"] != "" {
		port = config["port"]
	} else {
		port = "8080"
	}
	http.ListenAndServe(fmt.Sprintf(":%s", port), nil)
}
