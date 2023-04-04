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
	gtypes "github.com/gitopia/gitopia/x/gitopia/types"
	rtypes "github.com/gitopia/gitopia/x/rewards/types"
	otypes "github.com/gitopia/gitopia/x/offchain/types"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func initClientConfig() {
	config := sdk.GetConfig()
	config.SetBech32PrefixForAccount(
		GITOPIA_ACC_ADDRESS_PREFIX,
		GITOPIA_ACC_ADDRESS_PREFIX+sdk.PrefixPublic)
	//config.Seal()
}

func CommandInit(cmd *cobra.Command, appName string) error {
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
	clientCtx := client.GetClientContextFromCmd(cmd).
		WithCodec(marshaler).
		WithInterfaceRegistry(interfaceRegistry).
		WithAccountRetriever(authtypes.AccountRetriever{}).
		WithTxConfig(txCfg).
		WithInput(os.Stdin)
	// sets global flags for keys subcommand
	return client.SetCmdClientContext(cmd, clientCtx)
}

func GetClientContext(cmd *cobra.Command) (client.Context, error) {
	clientCtx := client.GetClientContextFromCmd(cmd)
	clientCtx = clientCtx.WithChainID(viper.GetString("CHAIN_ID"))
	clientCtx = clientCtx.WithNodeURI(viper.GetString("TM_ADDR"))
	c, err := client.NewClientFromNode(clientCtx.NodeURI)
	if err != nil {
		return clientCtx, errors.Wrap(err, "error creatig tm client")
	}
	clientCtx = clientCtx.WithClient(c)
	clientCtx = clientCtx.WithBroadcastMode(flags.BroadcastSync)
	clientCtx = clientCtx.WithSkipConfirmation(true)

	backend, err := cmd.Flags().GetString(flags.FlagKeyringBackend)
	if err != nil {
		return clientCtx, errors.Wrap(err, "error parsing keyring backend")
	}
	kr, err := client.NewKeyringFromBackend(clientCtx, backend)
	if err != nil {
		return clientCtx, errors.Wrap(err, "error creating keyring backend")
	}
	clientCtx = clientCtx.WithKeyring(kr)
	clientCtx = clientCtx.WithKeyringDir(viper.GetString("WORKING_DIR"))

	from, err := cmd.Flags().GetString(flags.FlagFrom)
	if err != nil {
		return clientCtx, errors.Wrap(err, "error parsing from flag")
	}
	fromAddr, fromName, _, err := client.GetFromFields(clientCtx, kr, from)
	if err != nil {
		return clientCtx, errors.Wrap(err, "error parsing from Addr")
	}

	clientCtx = clientCtx.WithFrom(from).WithFromAddress(fromAddr).WithFromName(fromName)
	return clientCtx, nil
}