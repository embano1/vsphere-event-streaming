package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/embano1/memlog"
	"github.com/embano1/vsphere/client"
	"github.com/embano1/vsphere/logger"
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
			ctx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()

			t.Setenv("VCENTER_URL", vimclient.URL().String())
			t.Setenv("VCENTER_INSECURE", "true")
			t.Setenv("VCENTER_SECRET_PATH", dir)

			vc, err := client.New(ctx)
			assert.NilError(t, err)

			log, err := memlog.New(ctx)
			assert.NilError(t, err)

			srv := server{
				http: &http.Server{
					Addr: "127.0.0.1:0",
				},
				vc:  vc,
				log: log,
			}

			err = run(ctx, &srv)
			assert.ErrorType(t, err, context.DeadlineExceeded)

			// check the server (log) received events from VC
			_, latest := log.Range(context.TODO())
			assert.Assert(t, latest != -1)

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
