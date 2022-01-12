package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"strconv"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/embano1/memlog"
	"github.com/embano1/vsphere/client"
	"github.com/embano1/vsphere/logger"
	"github.com/google/uuid"
	"github.com/julienschmidt/httprouter"
	"go.uber.org/zap"
)

const (
	// http defaults
	apiPath         = "/api/v1"
	readTimeout     = 3 * time.Second
	streamTimeout   = 5 * time.Minute // forces stream disconnect after this time
	shutdownTimeout = 5 * time.Second
	pageSize        = 50
	offsetKey       = "offset"
	watchKey        = "watch"
)

type server struct {
	http *http.Server
	vc   *client.Client // vsphere
	log  *memlog.Log
}

type envConfig struct {
	RecordSize  int           `envconfig:"LOG_MAX_RECORD_SIZE_BYTES" required:"true" default:"524288"`
	SegmentSize int           `envconfig:"LOG_MAX_SEGMENT_SIZE" required:"true" default:"1000"`
	StreamBegin time.Duration `envconfig:"VCENTER_STREAM_BEGIN" required:"true" default:"10m"`
	Port        int           `envconfig:"PORT" required:"true" default:"8080"`
	Debug bool `envconfig:"DEBUG" default:"false"`
}

func newServer(ctx context.Context, port int) (*server, error) {
	var srv server
	vc, err := client.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create vsphere client: %w", err)
	}
	srv.vc = vc

	router := httprouter.New()
	router.GET(apiPath+"/events", srv.getEvents(ctx))
	router.GET(apiPath+"/events/:id", srv.getEvent(ctx))
	router.GET(apiPath+"/range", srv.getRange(ctx))

	bindAddr := fmt.Sprintf("0.0.0.0:%d", port)
	h := http.Server{
		Addr:         bindAddr,
		Handler:      router,
		ReadTimeout:  readTimeout,
		WriteTimeout: streamTimeout,
	}
	srv.http = &h

	return &srv, nil
}

func (s *server) initializeLog(ctx context.Context, start memlog.Offset, segmentSize, recordSize int) error {
	if s.log != nil {
		return nil
	}

	opts := []memlog.Option{
		memlog.WithStartOffset(start),
		memlog.WithMaxSegmentSize(segmentSize),
		memlog.WithMaxRecordDataSize(recordSize),
	}
	ml, err := memlog.New(ctx, opts...)
	if err != nil {
		return fmt.Errorf("create log: %w", err)
	}
	s.log = ml

	return nil
}

func (s *server) stop(ctx context.Context) error {
	if err := s.http.Shutdown(ctx); err != nil {
		return err
	}
	return nil
}

// returns the last page
// if "watch=true" starts streaming from next (latest+1)
// if "watch=true" and a valid "offset" is specified starts streaming from offset
func (s *server) getEvents(ctx context.Context) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		var watch bool

		if val := r.FormValue(watchKey); val != "" {
			val = html.EscapeString(val)
			switch val {
			case "true":
				watch = true
			default:
				http.Error(w, "invalid watch parameter", http.StatusBadRequest)
				return
			}
		}

		if !watch {
			s.readEvents(ctx, w, r)
			return
		}

		s.streamEvents(ctx, w, r)
	}
}

func (s *server) streamEvents(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	log := logger.Get(ctx).With(zap.String("streamID", uuid.New().String()))
	log.Debug("new stream request")
	defer func() {
		log.Debug("stream stopped")
	}()

	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Error("writer does not implement flusher")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Connection", "Keep-Alive")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "application/json")

	rctx := r.Context()
	start := memlog.Offset(-1)

	if o := r.FormValue(offsetKey); o != "" {
		o = html.EscapeString(o)
		offset, err := strconv.Atoi(o)
		if err != nil {
			http.Error(w, "invalid offset", http.StatusBadRequest)
			return
		}
		start = memlog.Offset(offset)
	}

	if start == -1 {
		log.Debug("no start offset specified")
		earliest, latest := s.log.Range(rctx)
		log.Debug("current log range", zap.Any("earliest", earliest), zap.Any("latest", latest))
		start = latest + 1
	}

	log.Debug("starting stream", zap.Any("start", start))
	stream := s.log.Stream(rctx, start)

	for {
		// give a chance for server shutdown (not guaranteed)
		if ctx.Err() != nil {
			return
		}

		if rec, ok := stream.Next(); ok {
			b := rec.Data
			b = append(b, byte('\n'))
			data := string(b)
			_, err := io.WriteString(w, data)
			if err != nil {
				log.Error("write event", zap.Error(err))
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			log.Debug("sending event", zap.String("event", data))
			flusher.Flush()
			continue
		}
		break
	}

	if err := stream.Err(); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}

		if errors.Is(err, memlog.ErrOutOfRange) {
			http.Error(w, "invalid offset: "+err.Error(), http.StatusBadRequest)
			return
		}

		log.Error("failed to stream", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

func (s *server) readEvents(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	log := logger.Get(ctx)

	rctx := r.Context()
	earliest, latest := s.log.Range(rctx)

	// empty log
	if latest == -1 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	start := getStart(earliest, latest, pageSize)

	var events []ce.Event
	for i := start; i <= latest; i++ {
		record, err := s.log.Read(rctx, i)
		if err != nil {
			// client gone
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}

			// purged record, continue
			if errors.Is(err, memlog.ErrOutOfRange) {
				continue
			}

			log.Error("read record", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		var e ce.Event
		if err = json.Unmarshal(record.Data, &e); err != nil {
			log.Error("unmarshal event", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		events = append(events, e)
	}

	b, err := json.Marshal(events)
	if err != nil {
		log.Error("marshal events response", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, err = w.Write(b)
	if err != nil {
		log.Error("write response", zap.Error(err))
	}
}

func (s *server) getEvent(ctx context.Context) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		id := ps.ByName("id")
		offset, err := strconv.Atoi(id)
		if err != nil {
			http.Error(w, "invalid offset", http.StatusBadRequest)
			return
		}

		rctx := r.Context()
		rec, err := s.log.Read(rctx, memlog.Offset(offset))
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}

			if errors.Is(err, memlog.ErrOutOfRange) || errors.Is(err, memlog.ErrFutureOffset) {
				http.Error(w, "invalid offset: "+err.Error(), http.StatusBadRequest)
				return
			}

			logger.Get(ctx).Error("read record", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, err = io.WriteString(w, string(rec.Data))
		if err != nil {
			logger.Get(ctx).Error("write event", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
}

// 204 on empty log
func (s *server) getRange(ctx context.Context) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		type logRange struct {
			Earliest memlog.Offset `json:"earliest"`
			Latest   memlog.Offset `json:"latest"`
		}

		rctx := r.Context()
		earliest, latest := s.log.Range(rctx)

		if latest == -1 {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		lr := logRange{
			Earliest: earliest,
			Latest:   latest,
		}

		if err := json.NewEncoder(w).Encode(lr); err != nil {
			logger.Get(ctx).Error("marshal range response", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
}

func getStart(earliest, latest memlog.Offset, pageSize int) memlog.Offset {
	start := earliest
	if int(latest-earliest+1) > pageSize {
		start = latest - memlog.Offset(pageSize) + 1 // include latest in range
	}

	return start
}
