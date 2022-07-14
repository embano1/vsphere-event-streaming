package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/embano1/memlog"
	"github.com/embano1/vsphere/logger"
	"github.com/google/go-cmp/cmp"
	"github.com/julienschmidt/httprouter"
	"go.uber.org/zap/zaptest"
	"gotest.tools/v3/assert"
)

func Test_getRange(t *testing.T) {
	tests := []struct {
		name            string
		start           memlog.Offset
		size            int
		data            [][]byte
		wantCode        int
		wantContentType string
		wantRange       string // json
	}{
		{
			name:            "204 on empty log",
			start:           0,
			size:            10,
			data:            nil,
			wantCode:        http.StatusNoContent,
			wantContentType: "",
			wantRange:       "",
		},
		{
			name:            "returns range for not truncated log",
			start:           0,
			size:            10,
			data:            createData(5),
			wantCode:        http.StatusOK,
			wantContentType: "application/json",
			wantRange:       `{"earliest":0,"latest":4}`,
		},
		{
			name:            "returns range after truncated log",
			start:           0,
			size:            5, // current + history = 10
			data:            createData(20),
			wantCode:        http.StatusOK,
			wantContentType: "application/json",
			wantRange:       `{"earliest":10,"latest":19}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			opts := []memlog.Option{
				memlog.WithStartOffset(tc.start),
				memlog.WithMaxSegmentSize(tc.size),
			}
			log, err := memlog.New(ctx, opts...)
			assert.NilError(t, err)

			for _, v := range tc.data {
				_, err = log.Write(ctx, v)
				assert.NilError(t, err)
			}

			srv := server{
				log: log,
			}

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/range", nil)

			h := srv.getRange(ctx)
			h(rec, req, nil)

			assert.Equal(t, rec.Result().Header.Get("content-type"), tc.wantContentType)
			assert.Equal(t, rec.Result().StatusCode, tc.wantCode)
			assert.Equal(t, strings.TrimRight(rec.Body.String(), "\n"), tc.wantRange)
		})
	}
}

func Test_getEvent(t *testing.T) {
	tests := []struct {
		name            string
		start           memlog.Offset
		size            int
		data            [][]byte
		eventID         string
		wantCode        int
		wantContentType string
		want            string
	}{
		{
			name:            "400 future offset on empty log",
			start:           0,
			size:            10,
			data:            nil,
			eventID:         "3",
			wantCode:        http.StatusBadRequest,
			wantContentType: "text/plain; charset=utf-8",
			want:            "future offset",
		},
		{
			name:            "400 invalid offset on truncated log",
			start:           0,
			size:            5,
			data:            createData(20),
			eventID:         "3",
			wantCode:        http.StatusBadRequest,
			wantContentType: "text/plain; charset=utf-8",
			want:            "invalid offset",
		},
		{
			name:            "400 id is not a number",
			start:           0,
			size:            10,
			data:            createData(10),
			eventID:         "blabla",
			wantCode:        http.StatusBadRequest,
			wantContentType: "text/plain; charset=utf-8",
			want:            "invalid offset",
		},
		{
			name:            "200 returns event on not truncated log",
			start:           0,
			size:            10,
			data:            createData(10),
			eventID:         "3",
			wantCode:        http.StatusOK,
			wantContentType: "application/json",
			want:            "3",
		},
		{
			name:            "200 returns event on truncated log",
			start:           0,
			size:            5,
			data:            createData(20),
			eventID:         "11",
			wantCode:        http.StatusOK,
			wantContentType: "application/json",
			want:            "11",
		},
		{
			name:            "200 returns event on not truncated log with start offset 10",
			start:           10,
			size:            10,
			data:            createData(10),
			eventID:         "11",
			wantCode:        http.StatusOK,
			wantContentType: "application/json",
			want:            "1",
		},
		{
			name:            "200 returns event on truncated log with start offset 20",
			start:           20,
			size:            5,
			data:            createData(20),
			eventID:         "31",
			wantCode:        http.StatusOK,
			wantContentType: "application/json",
			want:            "11",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			opts := []memlog.Option{
				memlog.WithStartOffset(tc.start),
				memlog.WithMaxSegmentSize(tc.size),
			}
			log, err := memlog.New(ctx, opts...)
			assert.NilError(t, err)

			for _, v := range tc.data {
				_, err = log.Write(ctx, v)
				assert.NilError(t, err)
			}

			srv := server{
				log: log,
			}

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/events/"+tc.eventID, nil)

			h := srv.getEvent(ctx)
			h(rec, req, []httprouter.Param{{Key: "id", Value: tc.eventID}})

			assert.Equal(t, rec.Result().Header.Get("content-type"), tc.wantContentType)
			assert.Equal(t, rec.Result().StatusCode, tc.wantCode)
			if !assert.Check(t, strings.Contains(rec.Body.String(), tc.want)) {
				t.Logf("got vs want diff: %s", cmp.Diff(rec.Body.String(), tc.want))
			}
		})
	}
}

func Test_getEvents(t *testing.T) {
	tests := []struct {
		name            string
		start           memlog.Offset
		size            int
		data            [][]byte
		wantCode        int
		wantContentType string
	}{
		{
			name:            "204 on empty log",
			start:           0,
			size:            10,
			data:            nil,
			wantCode:        204,
			wantContentType: "",
		},
		{
			name:            "200, log with 3 entries, returns 3 results",
			start:           0,
			size:            10,
			data:            createData(3),
			wantCode:        200,
			wantContentType: "application/json",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			opts := []memlog.Option{
				memlog.WithStartOffset(tc.start),
				memlog.WithMaxSegmentSize(tc.size),
			}
			log, err := memlog.New(ctx, opts...)
			assert.NilError(t, err)

			var (
				want []ce.Event
				now  = time.Now().UTC()
			)

			for _, v := range tc.data {
				// getevents expects CloudEvents
				e := ce.NewEvent()
				e.SetID(string(v))
				e.SetType("test.event.v0")
				e.SetTime(now)
				e.SetSource("/test/source")
				err = e.SetData(ce.ApplicationJSON, string(v))
				assert.NilError(t, err)

				b, err := json.Marshal(e)
				assert.NilError(t, err)

				_, err = log.Write(ctx, b)
				assert.NilError(t, err)

				want = append(want, e)
			}

			srv := server{
				log: log,
			}

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/events", nil)

			h := srv.getEvents(ctx)
			h(rec, req, nil)

			assert.Equal(t, rec.Result().StatusCode, tc.wantCode)

			var got []ce.Event
			err = json.NewDecoder(rec.Body).Decode(&got)
			assert.Equal(t, rec.Result().Header.Get("content-type"), tc.wantContentType)
			assert.Assert(t, err == nil || err == io.EOF) // empty response throws EOF
			assert.DeepEqual(t, want, got)
		})
	}
}

func Test_streamEvents(t *testing.T) {
	tests := []struct {
		name            string
		start           memlog.Offset
		size            int
		data            [][]byte
		watchParam      string
		offsetParam     string
		wantCode        int
		wantContentType string
		wantResult      []byte
	}{
		{
			name:            "200, no data on empty log",
			start:           0,
			size:            10,
			data:            nil,
			watchParam:      "true",
			offsetParam:     "",
			wantCode:        200,
			wantContentType: "application/json",
			wantResult:      []byte{},
		},
		{
			name:            "400, invalid watch param specified",
			start:           0,
			size:            10,
			data:            nil,
			watchParam:      "invalid",
			offsetParam:     "",
			wantCode:        400,
			wantContentType: "text/plain; charset=utf-8",
			wantResult:      []byte("invalid watch parameter\n"),
		},
		{
			name:            "200, write 3 records to log, no offset specified, no data returned",
			start:           0,
			size:            10,
			data:            createData(3),
			watchParam:      "true",
			offsetParam:     "",
			wantCode:        200,
			wantContentType: "application/json",
			wantResult:      []byte{},
		},
		{
			name:            "200, write 3 records to log, offset 0, 3 records returned",
			start:           0,
			size:            10,
			data:            createData(3),
			watchParam:      "true",
			offsetParam:     "0",
			wantCode:        200,
			wantContentType: "application/json",
			wantResult:      []byte("0\n1\n2\n"),
		},
		{
			name:            "400, write 20 records to log with size 5, offset 0, out of range",
			start:           0,
			size:            5,
			data:            createData(20),
			watchParam:      "true",
			offsetParam:     "0",
			wantCode:        400,
			wantContentType: "text/plain; charset=utf-8",
			wantResult:      []byte("invalid offset: offset out of range\n"),
		},
		{
			name:            "400, write 15 records to log with size 5, offset 10, 5 records returned",
			start:           0,
			size:            5,
			data:            createData(15),
			watchParam:      "true",
			offsetParam:     "10",
			wantCode:        200,
			wantContentType: "application/json",
			wantResult:      []byte("10\n11\n12\n13\n14\n"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := logger.Set(context.Background(), zaptest.NewLogger(t))

			opts := []memlog.Option{
				memlog.WithStartOffset(tc.start),
				memlog.WithMaxSegmentSize(tc.size),
			}
			log, err := memlog.New(ctx, opts...)
			assert.NilError(t, err)

			for _, v := range tc.data {
				_, err = log.Write(ctx, v)
				assert.NilError(t, err)
			}

			srv := server{
				log: log,
			}

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/events", nil)

			ctx, cancel := context.WithTimeout(ctx, time.Millisecond*100)
			defer cancel()

			req = req.WithContext(ctx)
			q := req.URL.Query()
			q.Add(watchKey, tc.watchParam)

			if tc.offsetParam != "" {
				q.Add(offsetKey, tc.offsetParam)
			}
			req.URL.RawQuery = q.Encode()

			h := srv.getEvents(ctx)
			h(rec, req, nil)

			assert.Equal(t, rec.Result().StatusCode, tc.wantCode)
			assert.Equal(t, rec.Result().Header.Get("content-type"), tc.wantContentType)
			assert.Equal(t, rec.Body.String(), string(tc.wantResult))
		})
	}
}

func Test_getStart(t *testing.T) {
	type args struct {
		earliest memlog.Offset
		latest   memlog.Offset
		pageSize int
	}
	tests := []struct {
		name string
		args args
		want memlog.Offset
	}{
		{
			name: "empty log",
			args: args{
				earliest: -1,
				latest:   -1,
				pageSize: 50,
			},
			want: -1,
		},
		{
			name: "earliest 0, latest 10, page 50",
			args: args{
				earliest: 0,
				latest:   10,
				pageSize: 50,
			},
			want: 0,
		},
		{
			name: "earliest 0, latest 100, page 50",
			args: args{
				earliest: 0,
				latest:   100,
				pageSize: 50,
			},
			want: 51,
		},
		{
			name: "earliest 99, latest 100, page 50",
			args: args{
				earliest: 99,
				latest:   100,
				pageSize: 50,
			},
			want: 99,
		},
		{
			name: "earliest 99, latest 100, page 50",
			args: args{
				earliest: 99,
				latest:   100,
				pageSize: 50,
			},
			want: 99,
		},
		{
			name: "earliest 51, latest 89, page 50",
			args: args{
				earliest: 51,
				latest:   89,
				pageSize: 50,
			},
			want: 51,
		},
		{
			name: "earliest 151, latest 304, page 50",
			args: args{
				earliest: 151,
				latest:   304,
				pageSize: 50,
			},
			want: 255,
		},
		{
			name: "earliest 151, latest 304, page 10",
			args: args{
				earliest: 151,
				latest:   304,
				pageSize: 10,
			},
			want: 295,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getStart(tt.args.earliest, tt.args.latest, tt.args.pageSize); got != tt.want {
				t.Errorf("getStart() = %v, want %v", got, tt.want)
			}
		})
	}
}

// createData returns a slice of []byte with the number of elements specified by
// vals. The value of each element is the current index converted to a string
// starting at 0.
func createData(vals int) [][]byte {
	data := make([][]byte, vals)

	for i := 0; i < vals; i++ {
		data[i] = []byte(strconv.Itoa(i))
	}

	return data
}
