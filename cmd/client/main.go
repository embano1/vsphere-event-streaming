package main

import (
	"bufio"
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/embano1/vsphere/logger"
	"go.uber.org/zap"
)

var (
	address string
	watch   bool
	start   int
)

func main() {
	flag.StringVar(&address, "server", "http://localhost:8080/api/v1/events", "full stream server URL")
	flag.IntVar(&start, "start", 0, "start offset (0 for latest)")
	flag.BoolVar(&watch, "watch", false, "watch the event stream")
	flag.Parse()

	l, err := zap.NewDevelopment()
	if err != nil {
		panic("create logger: " + err.Error())
	}

	ctx := logger.Set(context.Background(), l)
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		l.Fatal("could not create request", zap.Error(err))
	}

	req.Header.Add("Transfer-Encoding", "chunked")

	q := req.URL.Query()

	var startOffset string
	if start != 0 {
		startOffset = strconv.Itoa(start)
	}
	if startOffset != "" {
		q.Add("offset", startOffset)
	}

	if watch {
		q.Add("watch", strconv.FormatBool(watch))
	}

	req.URL.RawQuery = q.Encode()

	c := http.Client{
		Timeout: time.Second * 10, // will also terminate watch after this time
	}
	res, err := c.Do(req)
	if err != nil {
		l.Fatal("could not send request", zap.Error(err))
	}
	defer func() {
		if err = res.Body.Close(); err != nil {
			l.Error("could not close response body", zap.Error(err))
		}
	}()

	if res.StatusCode > 299 {
		l.Fatal("could not read event stream", zap.Int("statusCode", res.StatusCode))
	}

	scanner := bufio.NewScanner(res.Body)
	for scanner.Scan() {
		l.Debug("received new event", zap.String("event", scanner.Text()))
	}

	if scanner.Err() != nil {
		l.Fatal("could not read response body", zap.Error(scanner.Err()))
	}
}
