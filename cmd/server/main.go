package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/embano1/memlog"
	"github.com/embano1/vsphere/event"
	"github.com/embano1/vsphere/logger"
	"github.com/kelseyhightower/envconfig"
	"github.com/vmware/govmomi/vim25/types"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

const (
	eventFormat = "vmware.vsphere.%s.v0"
)

var pollInterval = time.Second // poll vcenter events

func main() {
	var env envConfig
	if err := envconfig.Process("", &env); err != nil {
		panic("could not process environment variables: " + err.Error())
	}

	var l *zap.Logger
	if env.Debug {
		zapLogger, err := zap.NewDevelopment()
		if err != nil {
			panic("could not create logger: " + err.Error())
		}
		l = zapLogger

	} else {
		zapLogger, err := zap.NewProduction()
		if err != nil {
			panic("could not create logger: " + err.Error())
		}
		l = zapLogger
	}

	l = l.Named("eventstream")
	ctx := logger.Set(context.Background(), l)
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	srv, err := newServer(ctx, env.Port)
	if err != nil {
		l.Fatal("could not create server", zap.Error(err))
	}

	if err = run(ctx, srv); err != nil && !errors.Is(err, context.Canceled) {
		l.Fatal("could not run server", zap.Error(err))
	}
}

func run(ctx context.Context, srv *server) error {
	var env envConfig
	if err := envconfig.Process("", &env); err != nil {
		return fmt.Errorf("process environment variables: %w", err)
	}

	l := logger.Get(ctx)
	eg, egCtx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		<-egCtx.Done()

		// use fresh ctx to give clients time to disconnect
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		l.Debug("stopping server", zap.Duration("timeout", shutdownTimeout))
		defer cancel()
		if err := srv.stop(shutdownCtx); err != nil {
			l.Error("could not gracefully stop server", zap.Error(err))
		}
		return egCtx.Err()
	})

	var once sync.Once
	eg.Go(func() error {
		root := srv.vc.SOAP.ServiceContent.RootFolder
		mgr := srv.vc.Events
		source := srv.vc.SOAP.URL().String()
		start := types.EventFilterSpecByTime{
			BeginTime: types.NewTime(time.Now().UTC().Add(-5 * time.Minute)),
		}

		collector, err := event.NewHistoryCollector(egCtx, mgr, root, event.WithTime(&start))
		if err != nil {
			return fmt.Errorf("create event collector: %w", err)
		}

		l.Info("starting vsphere event collector", zap.Duration("begin", env.StreamBegin), zap.Duration("pollInterval", pollInterval))
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-egCtx.Done():
				return egCtx.Err()
			case <-ticker.C:
				events, err := collector.ReadNextEvents(egCtx, 50)
				if err != nil {
					return fmt.Errorf("read events: %w", err)
				}

				for _, e := range events {
					id := e.GetEvent().Key

					// set event ID as start offset
					once.Do(func() {
						l.Debug("initializing new log",
							zap.Int32("startOffset", id),
							zap.Int("maxSegmentSize", env.SegmentSize),
							zap.Int("maxRecordSize", env.RecordSize),
						)
						if err := srv.initializeLog(egCtx, memlog.Offset(id), env.SegmentSize, env.RecordSize); err != nil {
							l.Fatal("initialize log", zap.Error(err))
						}
					})

					cevent := ce.NewEvent()

					d := event.GetDetails(e)
					cevent.SetSource(source)
					cevent.SetID(strconv.Itoa(int(id)))
					cevent.SetType(fmt.Sprintf(eventFormat, d.Type))
					cevent.SetTime(e.GetEvent().CreatedTime)
					if err := cevent.SetData(ce.ApplicationJSON, e); err != nil {
						l.Error("set cloudevent data", zap.Error(err), zap.Any("event", e))
						return fmt.Errorf("set cloudevent data: %v", err)
					}
					cevent.SetExtension("eventclass", d.Class)

					b, err := json.Marshal(cevent)
					if err != nil {
						l.Error("marshal cloudevent to JSON", zap.Error(err), zap.String("event", cevent.String()))
						return fmt.Errorf("marshal cloudevent to JSON: %v", err)
					}

					offset, err := srv.log.Write(egCtx, b)
					if err != nil {
						return fmt.Errorf("write to log: %w", err)
					}
					l.Debug("wrote cloudevent to log",
						zap.Any("offset", offset),
						zap.String("event", cevent.String()),
						zap.Int("bytes", len(b)),
					)
				}
			}
		}
	})

	eg.Go(func() error {
		l.Info("starting http listener", zap.Int("port", env.Port))
		if err := srv.http.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve http: %w", err)
		}
		return nil
	})

	return eg.Wait()
}
