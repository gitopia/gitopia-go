package gitopia

import (
	"os"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/std"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/version"
	"github.com/cosmos/cosmos-sdk/x/auth/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	gtypes "github.com/gitopia/gitopia/x/gitopia/types"
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
	version.Name = appName // os keyring service name is same as version name
	initClientConfig()

	interfaceRegistry := codectypes.NewInterfaceRegistry()
	std.RegisterInterfaces(interfaceRegistry)
	cryptocodec.RegisterInterfaces(interfaceRegistry)
	authtypes.RegisterInterfaces(interfaceRegistry)
	interfaceRegistry.RegisterInterface(
		"cosmos.auth.v1beta1.AccountI",
		(*authtypes.AccountI)(nil),
		&authtypes.BaseAccount{},
		&authtypes.ModuleAccount{},
	)
	gtypes.RegisterInterfaces(interfaceRegistry)

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