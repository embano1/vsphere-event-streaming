package main

const (
	addr = "http://localhost:8080/events"
)

func main() {
	/* c := http.Client{}

	logger, err := zap.NewDevelopment()
	if err != nil {
		panic("create logger: " + err.Error())
	}

	ctx := logging.WithLogger(signals.NewContext(), logger.Sugar())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		logger.Fatal("create request", zap.Error(err))
	}

	req.Header.Add("Transfer-Encoding", "chunked")

	start := "3"
	q := req.URL.Query()
	q.Add("offset", start)
	req.URL.RawQuery = q.Encode()

	res, err := c.Do(req)
	if err != nil {
		logger.Fatal("send request", zap.Error(err))
	}
	defer func() {
		if err = res.Body.Close(); err != nil {
			logger.Error("close response body", zap.Error(err))
		}
	}()

	decoder := bufio.NewReader(res.Body)

	var (
		raw       []byte
		event     internal.Event
		decodeErr error
	)

	for {
		raw, decodeErr = decoder.ReadBytes('\n')
		if decodeErr != nil {
			if errors.Is(decodeErr, io.EOF) || errors.Is(decodeErr, context.Canceled) {
				break
			}
			logger.Fatal("decode response data", zap.Error(decodeErr))
		}

		// raw = bytes.TrimRight(raw, "\n")
		if err := json.Unmarshal(raw, &event); err != nil {
			logger.Fatal("unmarshal event", zap.Error(decodeErr))
		}

		// logger.Debug("new event", zap.String("event", string(raw)))
		logger.Debug("new event", zap.Any("event", event))
	} */
}
