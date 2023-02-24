package gitopia

import (
	"context"
	"time"

	"github.com/gitopia/gitopia-go/logger"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"github.com/tendermint/tendermint/libs/log"
	jsonrpcclient "github.com/tendermint/tendermint/rpc/jsonrpc/client"
	jsonrpctypes "github.com/tendermint/tendermint/rpc/jsonrpc/types"
)

const (
	TM_WS_PING_PERIOD   = 10 * time.Second
	TM_WS_MAX_RECONNECT = 3
)

var (
	mTmError = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: viper.GetString("APP_NAME"),
		Name:      "tm_errors",
		Help:      "Number of tm errors",
	}, []string{"error"})
)

type evenHandlerFunc func(context.Context, []byte) error

type WSEvents struct {
	wsc   *jsonrpcclient.WSClient
	query string
}

func NewWSEvents(ctx context.Context, query string) (*WSEvents, error) {
	wse := &WSEvents{
		query: query,
	}

	var err error
	wse.wsc, err = jsonrpcclient.NewWS(viper.GetString("TM_ADDR"),
		TM_WS_ENDPOINT,
		jsonrpcclient.PingPeriod(TM_WS_PING_PERIOD),
		jsonrpcclient.MaxReconnectAttempts(TM_WS_MAX_RECONNECT),
		jsonrpcclient.OnReconnect(func() {
			// resubscribe immediately
			wse.subscribeAfter(0 * time.Second)
		}))
	if err != nil {
		return nil, errors.Wrap(err, "error creating ws client")
	}

	w := logger.FromContext(ctx).WriterLevel(logrus.DebugLevel)
	l := log.NewTMLogger(log.NewSyncWriter(w))
	wse.wsc.SetLogger(l)

	if err := wse.wsc.Start(); err != nil {
		return nil, errors.Wrap(err, "error connecting to WS")
	}

	return wse, nil
}

func terminateOnCancel(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return nil
}

// processes events from tm
// cancel context to stop processing
// returns error on failure
// returns error when event handler returns error
// handler must handle all errors. it must return only fatal errors
func (wse *WSEvents) Subscribe(ctx context.Context, h evenHandlerFunc) (<-chan struct{}, chan error) {
	e := make(chan error)
	done := make(chan struct{})

	go func() {
		defer func() { close(done) }()
		logger.FromContext(ctx).Debug("subscribing to tm")
		defer logger.FromContext(ctx).Debug("subscription done")

		err := wse.wsc.Subscribe(ctx, wse.query)
		if err != nil {
			e <- errors.Wrap(err, "error sending subscribe request")
			return
		}

		//!! CAUTION!! all events are processed sequentially in order to support backfill!
		// this might lead to event queue overflow on the chain and connection disconnection
		for {
			err = terminateOnCancel(ctx)
			if err != nil {
				e <- err
				return
			}
			var event jsonrpctypes.RPCResponse
			select {
			case event = <-wse.wsc.ResponsesCh:
			case <-wse.wsc.Quit():
				e <- errors.New("ws conn closed")
				return
			}
			if event.Error != nil {
				logger.FromContext(ctx).Error("WS error", "err", event.Error.Error())
				// Error can be ErrAlreadySubscribed or max client (subscriptions per
				// client) reached or Tendermint exited.
				// We can ignore ErrAlreadySubscribed, but need to retry in other
				// cases.
				// if !isErrAlreadySubscribed(event.Error) {
				// 	// Resubscribe after 1 second to give Tendermint time to restart (if
				// 	// crashed).
				// 	wse.subscribeAfter(1 * time.Second)
				// }
				// OnReconnect handles this
				mTmError.With(prometheus.Labels{"error": "ws_event_error"}).Inc()
				continue
			}

			jsonBuf, err := event.Result.MarshalJSON()
			if err != nil {
				logger.FromContext(ctx).WithError(err).WithField("result", event.Result).
					Error("error parsing result. ignoring event")
				mTmError.With(prometheus.Labels{"error": "parse_error"}).Inc()
				continue
			}
			// hack: TM sends empty event to begin with. skipping
			if string(jsonBuf) == "{}" {
				logger.FromContext(ctx).Info("received empty event. continuing...")
				continue
			}
			err = h(ctx, jsonBuf)
			if err != nil {
				logger.FromContext(ctx).Error(errors.WithMessage(err, "error from event handler"))
				mTmError.With(prometheus.Labels{"error": "handler_error"}).Inc()
				e <- err
				return
			}
		}
	}()
	return ctx.Done(), e
}

func (wse *WSEvents) Unsubscribe(ctx context.Context, query string) error {
	if err := wse.wsc.Unsubscribe(ctx, query); err != nil {
		return err
	}

	return nil
}

// After being reconnected, it is necessary to redo subscription to server
// otherwise no data will be automatically received.
func (wse *WSEvents) subscribeAfter(d time.Duration) {
	time.Sleep(d)

	err := wse.wsc.Subscribe(context.Background(), wse.query)
	if err != nil {
		wse.wsc.Logger.Error("Failed to resubscribe", "err", err)
	}
}
