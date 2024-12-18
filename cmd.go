package gitopia

import (
	"os"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/std"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/version"
	"github.com/cosmos/cosmos-sdk/x/auth/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	gtypes "github.com/gitopia/gitopia/v5/x/gitopia/types"
	otypes "github.com/gitopia/gitopia/v5/x/offchain/types"
	rtypes "github.com/gitopia/gitopia/v5/x/rewards/types"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

func initClientConfig() {
	config := sdk.GetConfig()
	config.SetBech32PrefixForAccount(
		GITOPIA_ACC_ADDRESS_PREFIX,
		GITOPIA_ACC_ADDRESS_PREFIX+sdk.PrefixPublic)
	//config.Seal()
}

func CommandInit(cmd *cobra.Command, appName string) error {
	// configure custom fields
	clientCtx, err := GetClientContext(appName)
	if err != nil {
		return errors.Wrap(err, "error getting client context")
	}

	// set to read tx flags
	err = client.SetCmdClientContext(cmd, clientCtx)
	if err != nil {
		return errors.Wrap(err, "error getting client context")
	}

	// read tx flags
	clientCtx, err = client.GetClientTxContext(cmd)
	if err != nil {
		return errors.Wrap(err, "error getting client context")
	}

	// sets global flags for keys subcommand
	return client.SetCmdClientContext(cmd, clientCtx)
}

func GetClientContext(appName string) (client.Context, error) {
	version.Name = appName // os keyring service name is same as version name
	initClientConfig()

	interfaceRegistry := codectypes.NewInterfaceRegistry()
	std.RegisterInterfaces(interfaceRegistry)
	cryptocodec.RegisterInterfaces(interfaceRegistry)
	authtypes.RegisterInterfaces(interfaceRegistry)
	gtypes.RegisterInterfaces(interfaceRegistry)
	rtypes.RegisterInterfaces(interfaceRegistry)
	otypes.RegisterInterfaces(interfaceRegistry)

	marshaler := codec.NewProtoCodec(interfaceRegistry)
	txCfg := tx.NewTxConfig(marshaler, tx.DefaultSignModes)
	clientCtx := client.Context{}.
		WithCodec(marshaler).
		WithInterfaceRegistry(interfaceRegistry).
		WithAccountRetriever(authtypes.AccountRetriever{}).
		WithTxConfig(txCfg).
		WithInput(os.Stdin)

	clientCtx = clientCtx.WithChainID(CHAIN_ID)
	clientCtx = clientCtx.WithNodeURI(TM_ADDR)
	c, err := client.NewClientFromNode(clientCtx.NodeURI)
	if err != nil {
		return clientCtx, errors.Wrap(err, "error creating tm client")
	}
	clientCtx = clientCtx.WithClient(c)
	clientCtx = clientCtx.WithBroadcastMode(flags.BroadcastSync)
	clientCtx = clientCtx.WithSkipConfirmation(true)
	clientCtx = clientCtx.WithKeyringDir(WORKING_DIR)

	if FEE_GRANTER_ADDR != "" {
		feeGranterAddr := sdk.MustAccAddressFromBech32(FEE_GRANTER_ADDR)
		clientCtx = clientCtx.WithFeeGranterAddress(feeGranterAddr)
	}

	return clientCtx, nil
}

func GetClientContextWithOptions(appName string, kb string, from string) (client.Context, error) {
	clientCtx, err := GetClientContext(appName)
	if err != nil {
		return client.Context{}, err
	}

	kr, err := client.NewKeyringFromBackend(clientCtx, kb)
	if err != nil {
		return clientCtx, errors.Wrap(err, "error creating keyring backend")
	}
	clientCtx = clientCtx.WithKeyring(kr)

	fromAddr, fromName, _, err := client.GetFromFields(clientCtx, kr, from)
	if err != nil {
		return clientCtx, errors.Wrap(err, "error parsing from Addr")
	}

	clientCtx = clientCtx.WithFrom(from).WithFromAddress(fromAddr).WithFromName(fromName)
	return clientCtx, nil
}
