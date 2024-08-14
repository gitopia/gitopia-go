package gitopia

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	rpcclient "github.com/cometbft/cometbft/rpc/client"
	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	ctypes "github.com/cometbft/cometbft/rpc/core/types"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	"github.com/gitopia/gitopia-go/logger"
	gtypes "github.com/gitopia/gitopia/v4/x/gitopia/types"
	rtypes "github.com/gitopia/gitopia/v4/x/rewards/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	GITOPIA_ACC_ADDRESS_PREFIX = "gitopia"
	GAS_ADJUSTMENT             = 1.5
	MAX_TRIES                  = 5
	MAX_WAIT_BLOCKS            = 10
	TM_WS_ENDPOINT             = "/websocket"
)

type Query struct {
	Gitopia gtypes.QueryClient
	Rewards rtypes.QueryClient
}
type Client struct {
	cc  client.Context
	txf tx.Factory
	rc  rpcclient.Client
	w   *io.PipeWriter

	Query
}

func NewClient(ctx context.Context, cc client.Context, txf tx.Factory) (Client, error) {
	w := logger.FromContext(ctx).WriterLevel(logrus.DebugLevel)
	cc = cc.WithOutput(w)

	rc, err := rpchttp.New(cc.NodeURI, TM_WS_ENDPOINT)
	if err != nil {
		return Client{}, errors.Wrap(err, "error creating rpc client")
	}

	q, err := GetQueryClient(GITOPIA_ADDR)
	if err != nil {
		return Client{}, errors.Wrap(err, "error creating query client")
	}

	return Client{
		cc:  cc,
		txf: txf,
		rc:  rc,
		w:   w,

		Query: q,
	}, nil
}

func GetQueryClient(addr string) (Query, error) {
	grpcConn, err := grpc.Dial(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(codec.NewProtoCodec(nil).GRPCCodec())),
	)
	if err != nil {
		return Query{}, errors.Wrap(err, "error connecting to gitopia")
	}

	gqc := gtypes.NewQueryClient(grpcConn)
	rqc := rtypes.NewQueryClient(grpcConn)
	return Query{gqc, rqc}, nil
}

// implement io.Closer
func (c Client) Close() error {
	return c.w.Close()
}

func (c Client) QueryClient() *Query {
	return &c.Query
}

func (c Client) Address() sdk.AccAddress {
	return c.cc.FromAddress
}

func (c Client) AuthorizedBroadcastTx(ctx context.Context, msg sdk.Msg) error {
	execMsg := authz.NewMsgExec(c.cc.FromAddress, []sdk.Msg{msg})

	// !!HACK!! set sequence to 0 to force refresh account sequence for every txn
	txHash, err := BroadcastTx(c.cc, c.txf.WithSequence(0).WithFeePayer(c.Address()), &execMsg)
	if err != nil {
		return err
	}

	_, err = c.waitForTx(ctx, txHash)
	if err != nil {
		return errors.Wrap(err, "error waiting for tx"+txHash)
	}

	return nil
}

func (c Client) BroadcastTxAndWait(ctx context.Context, msg ...sdk.Msg) error {
	// !!HACK!! set sequence to 0 to force refresh account sequence for every txn
	txHash, err := BroadcastTx(c.cc, c.txf.WithSequence(0).WithFeePayer(c.Address()), msg...)
	if err != nil {
		return err
	}

	_, err = c.waitForTx(ctx, txHash)
	if err != nil {
		return errors.Wrap(err, "error waiting for tx"+txHash)
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
	fees, err := calculateFee(adjusted)
	if err != nil {
		return "", err
	}

	txf = txf.WithGas(adjusted).WithFees(fees.String())

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
func (c Client) Status(ctx context.Context) (*ctypes.ResultStatus, error) {
	return c.rc.Status(ctx)
}

// latestBlockHeight returns the lastest block height of the app.
func (c Client) latestBlockHeight(ctx context.Context) (int64, error) {
	resp, err := c.Status(ctx)
	if err != nil {
		return 0, err
	}
	return resp.SyncInfo.LatestBlockHeight, nil
}

// waitForNextBlock waits until next block is committed.
// It reads the current block height and then waits for another block to be
// committed, or returns an error if ctx is canceled.
func (c Client) waitForNextBlock(ctx context.Context) error {
	return c.waitForNBlocks(ctx, 1)
}

// waitForNBlocks reads the current block height and then waits for anothers n
// blocks to be committed, or returns an error if ctx is canceled.
func (c Client) waitForNBlocks(ctx context.Context, n int64) error {
	start, err := c.latestBlockHeight(ctx)
	if err != nil {
		return err
	}
	return c.waitForBlockHeight(ctx, start+n)
}

// waitForBlockHeight waits until block height h is committed, or returns an
// error if ctx is canceled.
func (c Client) waitForBlockHeight(ctx context.Context, h int64) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for i := 0; i < MAX_TRIES; i++ {
		latestHeight, err := c.latestBlockHeight(ctx)
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
func (c Client) waitForTx(ctx context.Context, hash string) (*ctypes.ResultTx, error) {
	bz, err := hex.DecodeString(hash)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to decode tx hash '%s'", hash)
	}
	for i := 0; i < MAX_WAIT_BLOCKS; i++ {
		resp, err := c.rc.Tx(ctx, bz, false)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				// Tx not found, wait for next block and try again
				err := c.waitForNextBlock(ctx)
				if err != nil && !strings.Contains(err.Error(), "timeout") {
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

func calculateFee(gas uint64) (sdk.Coins, error) {
	gasPrice, err := sdk.ParseDecCoin(GAS_PRICES)
	if err != nil {
		return nil, err
	}
	fee := float64(gas) * float64(gasPrice.Amount.MustFloat64())
	fee = math.Ceil(fee)

	return sdk.NewCoins(sdk.NewCoin(gasPrice.Denom, sdk.NewInt(int64(fee)))), nil
}
