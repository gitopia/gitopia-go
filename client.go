package gitopia

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	"github.com/gitopia/git-server/logger"
	"github.com/gitopia/gitopia/x/gitopia/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	rpcclient "github.com/tendermint/tendermint/rpc/client"
	rpchttp "github.com/tendermint/tendermint/rpc/client/http"
	ctypes "github.com/tendermint/tendermint/rpc/core/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	GITOPIA_ACC_ADDRESS_PREFIX = "gitopia"
	GAS_ADJUSTMENT             = 1.5
	MAX_TRIES                  = 5
	MAX_WAIT_BLOCKS            = 10
)

type Client struct {
	cc  client.Context
	txf tx.Factory
	qc  types.QueryClient
	rc  rpcclient.Client
	w   *io.PipeWriter
}

func NewClient(ctx context.Context, cc client.Context, txf tx.Factory) (Client, error) {
	w := logger.FromContext(ctx).WriterLevel(logrus.DebugLevel)
	cc = cc.WithOutput(w)

	txf = txf.WithGasPrices(viper.GetString("gas_prices")).WithGasAdjustment(GAS_ADJUSTMENT)

	grpcConn, err := grpc.Dial(viper.GetString("gitopia_grpc_url"),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(codec.NewProtoCodec(nil).GRPCCodec())),
	)
	if err != nil {
		return Client{}, errors.Wrap(err, "error creating grpc client")
	}

	qc := types.NewQueryClient(grpcConn)

	rc, err := rpchttp.New(cc.NodeURI, "/websocket")
	if err != nil {
		return Client{}, errors.Wrap(err, "error creating rpc client")
	}

	return Client{
		cc:  cc,
		txf: txf,
		qc:  qc,
		rc:  rc,
		w:   w,
	}, nil
}

// implement io.Closer
func (g Client) Close() error {
	return g.w.Close()
}

func (g Client) QueryClient() types.QueryClient {
	return g.qc
}

func (g Client) Address() string {
	return g.cc.FromAddress.String()
}

func (g Client) AuthorizedBroadcastTx(ctx context.Context, msg sdk.Msg) error {
	execMsg := authz.NewMsgExec(g.cc.FromAddress, []sdk.Msg{msg})
	// !!HACK!! set sequence to 0 to force refresh account sequence for every txn
	txHash, err := BroadcastTx(g.cc, g.txf.WithSequence(0), &execMsg)
	if err != nil {
		return err
	}

	_, err = g.waitForTx(ctx, txHash)
	if err != nil {
		return errors.Wrap(err, "error waiting for tx")
	}

	return nil
}

// BroadcastTx attempts to generate, sign and broadcast a transaction with the
// given set of messages.
// It will return an error upon failure.
func BroadcastTx(clientCtx client.Context, txf tx.Factory, msgs ...sdk.Msg) (string, error) {
	txf, err := txf.Prepare(clientCtx)
	if err != nil {
		return "", err
	}

	_, adjusted, err := tx.CalculateGas(clientCtx, txf, msgs...)
	if err != nil {
		return "", err
	}

	txf = txf.WithGas(adjusted)

	txn, err := txf.BuildUnsignedTx(msgs...)
	if err != nil {
		return "", err
	}

	err = tx.Sign(txf, clientCtx.GetFromName(), txn, true)
	if err != nil {
		return "", err
	}

	txBytes, err := clientCtx.TxConfig.TxEncoder()(txn.GetTx())
	if err != nil {
		return "", err
	}

	// broadcast to a Tendermint node
	res, err := clientCtx.BroadcastTx(txBytes)
	if err != nil {
		return "", err
	}

	return res.TxHash, nil
}

// Status returns the node Status
func (g Client) Status(ctx context.Context) (*ctypes.ResultStatus, error) {
	return g.rc.Status(ctx)
}

// latestBlockHeight returns the lastest block height of the app.
func (g Client) latestBlockHeight(ctx context.Context) (int64, error) {
	resp, err := g.Status(ctx)
	if err != nil {
		return 0, err
	}
	return resp.SyncInfo.LatestBlockHeight, nil
}

// waitForNextBlock waits until next block is committed.
// It reads the current block height and then waits for another block to be
// committed, or returns an error if ctx is canceled.
func (g Client) waitForNextBlock(ctx context.Context) error {
	return g.waitForNBlocks(ctx, 1)
}

// waitForNBlocks reads the current block height and then waits for anothers n
// blocks to be committed, or returns an error if ctx is canceled.
func (g Client) waitForNBlocks(ctx context.Context, n int64) error {
	start, err := g.latestBlockHeight(ctx)
	if err != nil {
		return err
	}
	return g.waitForBlockHeight(ctx, start+n)
}

// waitForBlockHeight waits until block height h is committed, or returns an
// error if ctx is canceled.
func (g Client) waitForBlockHeight(ctx context.Context, h int64) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for i := 0; i < MAX_TRIES; i++ {
		latestHeight, err := g.latestBlockHeight(ctx)
		if err != nil {
			return err
		}
		if latestHeight >= h {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Wrap(ctx.Err(), "context is cancelled")
		case <-ticker.C:
		}
	}

	return fmt.Errorf("timeout error")
}

// waitForTx requests the tx from hash, if not found, waits for next block and
// tries again. Returns an error if ctx is canceled.
func (g Client) waitForTx(ctx context.Context, hash string) (*ctypes.ResultTx, error) {
	bz, err := hex.DecodeString(hash)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to decode tx hash '%s'", hash)
	}
	for i := 0; i < MAX_WAIT_BLOCKS; i++ {
		resp, err := g.rc.Tx(ctx, bz, false)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				// Tx not found, wait for next block and try again
				err := g.waitForNextBlock(ctx)
				if err != nil {
					return nil, errors.Wrap(err, "waiting for next block")
				}
				continue
			}
			return nil, errors.Wrapf(err, "fetching tx '%s'", hash)
		}
		// Tx found
		return resp, nil
	}

	return nil, fmt.Errorf("max block wait exceeded")
}
