package rocket

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
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

type resp struct {
	err error
	str string
}

func fetch(host string, urlStr string, req *http.Request, c chan resp) {
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

		log.Printf("%s: proxy error: %v", urlStr, err)
		c <- struct {
			err error
			str string
		}{err, ""}
		return
	}

	defer res.Body.Close()

	if res.StatusCode != 200 {
		log.Printf("%d %s", res.StatusCode, urlStr)
		c <- resp{errors.New(fmt.Sprintf("Error fetch %s: %v", urlStr, res.StatusCode)), ""}
		return
	}

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		c <- resp{err, ""}
		return
	}

	log.Printf("%d %s", res.StatusCode, urlStr)

	c <- resp{nil, fmt.Sprintf("{\"url\":\"%s\",\"data\":%s}", urlStr, string(body))}
}

func getBundle(path string) string {
	parts := strings.Split(path, "/")
	last := parts[len(parts)-1]
	return strings.Split(last, ".")[0]
}

type RocketServer struct {
	bundles map[string][]string
	host string
}

func (rs RocketServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	bundle := getBundle(r.URL.Path)

	var urls []string

	if rs.bundles[bundle] != nil {
		urls = rs.bundles[bundle]
	} else {
		urls = rs.bundles["bootstrap"]
	}

	c := make(chan resp)

	for _, url := range urls {
		go fetch(rs.host, url, r, c)
	}

	header := w.Header()
	header.Set("Content-Type", "application/json")

	fmt.Fprintf(w, "{\"responses\":[")

	num := len(urls)

	for i := 0; i < num; i++ {
		resp, ok := <-c
		if ok {
			err, str := resp.err, resp.str
			if err == nil {
				fmt.Fprintf(w, "%s,", str)
			}
		} else {
			log.Printf("read error")
		}
	}

	fmt.Fprintf(w, "{}")

	fmt.Fprintf(w, "]}")	
}

func New(host string, bundles map[string][]string) RocketServer {
	return RocketServer{host: host, bundles: bundles};
}