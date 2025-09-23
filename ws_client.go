package gitopia

import (
	"context"
	"sync"
	"time"

	jsonrpcclient "github.com/cometbft/cometbft/rpc/jsonrpc/client"
	jsonrpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
	"github.com/gitopia/gitopia-go/logger"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/spf13/viper"
)

const (
	TM_WS_PING_PERIOD   = 10 * time.Second
	TM_WS_MAX_RECONNECT = -1 // Unlimited reconnection attempts
	TM_WS_RECONNECT_DELAY = 30 * time.Second // Delay between reconnection attempts
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
	wsc      *jsonrpcclient.WSClient
	endpoint string          // Store endpoint for reconnection
	queries  map[string]bool // Track active subscriptions
	mu       sync.RWMutex    // Protect queries map
}

func NewWSEvents(ctx context.Context) (*WSEvents, error) {
	return NewWSEventsWithEndpoint(ctx, viper.GetString("TM_ADDR"))
}

func NewWSEventsWithEndpoint(ctx context.Context, endpoint string) (*WSEvents, error) {
	wse := &WSEvents{
		endpoint: endpoint,
		queries:  make(map[string]bool),
	}

	var err error
	wse.wsc, err = jsonrpcclient.NewWS(endpoint,
		TM_WS_ENDPOINT,
		jsonrpcclient.PingPeriod(TM_WS_PING_PERIOD),
		jsonrpcclient.MaxReconnectAttempts(TM_WS_MAX_RECONNECT),
		jsonrpcclient.OnReconnect(func() {
			// Resubscribe to all queries after reconnection
			wse.resubscribeAll()
		}))
	if err != nil {
		return nil, errors.Wrap(err, "error creating ws client")
	}

	if err := wse.wsc.Start(); err != nil {
		return nil, errors.Wrap(err, "error connecting to WS")
	}

	return wse, nil
}

// Subscribe to a single query
func (wse *WSEvents) SubscribeQuery(ctx context.Context, query string) error {
	wse.mu.Lock()
	defer wse.mu.Unlock()

	// Avoid duplicate subscriptions
	if wse.queries[query] {
		return nil // Already subscribed
	}

	err := wse.wsc.Subscribe(ctx, query)
	if err != nil {
		return errors.Wrap(err, "error sending subscribe request")
	}

	wse.queries[query] = true
	return nil
}

// Subscribe to multiple queries at once
func (wse *WSEvents) SubscribeQueries(ctx context.Context, queries ...string) error {
	for _, query := range queries {
		if err := wse.SubscribeQuery(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

// Unsubscribe from a specific query
func (wse *WSEvents) UnsubscribeQuery(ctx context.Context, query string) error {
	wse.mu.Lock()
	defer wse.mu.Unlock()

	if !wse.queries[query] {
		return nil // Not subscribed
	}

	if err := wse.wsc.Unsubscribe(ctx, query); err != nil {
		return err
	}

	delete(wse.queries, query)
	return nil
}

// Get list of active subscriptions
func (wse *WSEvents) GetActiveQueries() []string {
	wse.mu.RLock()
	defer wse.mu.RUnlock()

	queries := make([]string, 0, len(wse.queries))
	for query := range wse.queries {
		queries = append(queries, query)
	}
	return queries
}

func terminateOnCancel(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return nil
}

// ProcessEvents handles events from all subscribed queries with automatic reconnection
// The handler receives all events and must filter/route them as needed
func (wse *WSEvents) ProcessEvents(ctx context.Context, h evenHandlerFunc) (<-chan struct{}, chan error) {
	e := make(chan error)
	done := make(chan struct{})

	go func() {
		defer func() { close(done) }()
		logger.FromContext(ctx).Debug("processing tm events")
		defer logger.FromContext(ctx).Debug("event processing done")

		for {
			err := wse.processEventsLoop(ctx, h)
			if err != nil {
				if ctx.Err() != nil {
					// Context cancelled, exit gracefully
					e <- ctx.Err()
					return
				}
				
				logger.FromContext(ctx).WithError(err).Error("WebSocket connection lost, attempting to reconnect")
				mTmError.With(prometheus.Labels{"error": "connection_lost"}).Inc()
				
				// Attempt to reconnect
				if reconnectErr := wse.reconnect(ctx); reconnectErr != nil {
					logger.FromContext(ctx).WithError(reconnectErr).Error("Failed to reconnect WebSocket")
					time.Sleep(TM_WS_RECONNECT_DELAY)
					continue
				}
				
				logger.FromContext(ctx).Info("WebSocket reconnected successfully")
			}
		}
	}()
	return ctx.Done(), e
}

// processEventsLoop handles the main event processing loop
func (wse *WSEvents) processEventsLoop(ctx context.Context, h evenHandlerFunc) error {
	//!! CAUTION!! all events are processed sequentially in order to support backfill!
	// this might lead to event queue overflow on the chain and connection disconnection
	for {
		err := terminateOnCancel(ctx)
		if err != nil {
			return err
		}

		var event jsonrpctypes.RPCResponse
		select {
		case event = <-wse.wsc.ResponsesCh:
		case <-wse.wsc.Quit():
			return errors.New("ws conn closed")
		case <-ctx.Done():
			return ctx.Err()
		}

		if event.Error != nil {
			logger.FromContext(ctx).Error("WS error", "err", event.Error.Error())
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
			// logger.FromContext(ctx).Info("received empty event. continuing...")
			continue
		}

		err = h(ctx, jsonBuf)
		if err != nil {
			logger.FromContext(ctx).Error(errors.WithMessage(err, "error from event handler"))
			mTmError.With(prometheus.Labels{"error": "handler_error"}).Inc()
			// Don't return on handler errors, just log and continue
			continue
		}
	}
}

// reconnect attempts to reconnect the WebSocket client
func (wse *WSEvents) reconnect(ctx context.Context) error {
	// Close existing connection
	if wse.wsc != nil {
		wse.wsc.Stop()
	}
	
	// Create new WebSocket client using stored endpoint
	var err error
	wse.wsc, err = jsonrpcclient.NewWS(wse.endpoint,
		TM_WS_ENDPOINT,
		jsonrpcclient.PingPeriod(TM_WS_PING_PERIOD),
		jsonrpcclient.MaxReconnectAttempts(TM_WS_MAX_RECONNECT),
		jsonrpcclient.OnReconnect(func() {
			// Resubscribe to all queries after reconnection
			wse.resubscribeAll()
		}))
	if err != nil {
		return errors.Wrap(err, "error creating new ws client")
	}

	if err := wse.wsc.Start(); err != nil {
		return errors.Wrap(err, "error starting new ws client")
	}
	
	// Resubscribe to all active queries
	wse.resubscribeAll()
	
	return nil
}

// Resubscribe to all active queries (used after reconnection)
func (wse *WSEvents) resubscribeAll() {
	wse.mu.RLock()
	queries := make([]string, 0, len(wse.queries))
	for query := range wse.queries {
		queries = append(queries, query)
	}
	wse.mu.RUnlock()

	time.Sleep(100 * time.Millisecond) // Small delay to ensure connection is ready

	for _, query := range queries {
		err := wse.wsc.Subscribe(context.Background(), query)
		if err != nil {
			wse.wsc.Logger.Error("Failed to resubscribe", "query", query, "err", err)
		} else {
			wse.wsc.Logger.Info("Resubscribed successfully", "query", query)
		}
	}
}

// Close the connection and cleanup
func (wse *WSEvents) Close() error {
	return wse.wsc.Stop()
}
