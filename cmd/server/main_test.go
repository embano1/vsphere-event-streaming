package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/embano1/vsphere/client"
	"github.com/embano1/vsphere/logger"
	"github.com/julienschmidt/httprouter"
	"github.com/vmware/govmomi/simulator"
	_ "github.com/vmware/govmomi/vapi/simulator"
	"github.com/vmware/govmomi/vim25"
	"go.uber.org/zap/zaptest"
	"gotest.tools/v3/assert"
)

const (
	userFileKey     = "username"
	passwordFileKey = "password"
)

func Test_run(t *testing.T) {
	t.Run("successfully shuts down server", func(t *testing.T) {
		dir := tempDir(t)

		t.Cleanup(func() {
			err := os.RemoveAll(dir)
			assert.NilError(t, err)
		})

		simulator.Run(func(ctx context.Context, vimclient *vim25.Client) error {
			ctx = logger.Set(ctx, zaptest.NewLogger(t))

			pollInterval = time.Millisecond * 10
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()

			t.Setenv("VCENTER_URL", vimclient.URL().String())
			t.Setenv("VCENTER_INSECURE", "true")
			t.Setenv("VCENTER_SECRET_PATH", dir)

			vc, err := client.New(ctx)
			assert.NilError(t, err)

			const address = "127.0.0.1:8080"
			srv, err := newServer(ctx, address)
			assert.NilError(t, err)

			srv.vc = vc

			runErrCh := make(chan error)
			go func() {
				runErrCh <- run(ctx, srv)
			}()

			// give server time to initialize event stream log
			time.Sleep(time.Second)

			wanteventID := "20"
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(
				http.MethodGet,
				fmt.Sprintf("http://%s/api/v1/events/%s", address, wanteventID),
				nil,
			)

			h := srv.getEvent(ctx)
			h(rec, req, httprouter.Params{{
				Key:   "id",
				Value: wanteventID,
			}})

			assert.Equal(t, rec.Code, http.StatusOK)

			var gotevent ce.Event
			err = json.NewDecoder(rec.Body).Decode(&gotevent)
			assert.NilError(t, err)
			assert.NilError(t, gotevent.Validate())
			assert.Equal(t, gotevent.ID(), wanteventID)

			// stop server
			cancel()
			err = <-runErrCh
			assert.ErrorContains(t, err, "context canceled")

			return nil
		})
	})
}

func tempDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "")
	assert.NilError(t, err)

	f, err := os.Create(filepath.Join(dir, userFileKey))
	assert.NilError(t, err)

	_, err = f.Write([]byte("user"))
	assert.NilError(t, err)
	assert.NilError(t, f.Close())

	f, err = os.Create(filepath.Join(dir, passwordFileKey))
	assert.NilError(t, err)

	_, err = f.Write([]byte("pass"))
	assert.NilError(t, err)
	assert.NilError(t, f.Close())

	return dir
}
