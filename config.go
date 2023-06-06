package gitopia

var APP_NAME string
var GAS_PRICES string
var GITOPIA_ADDR string
var CHAIN_ID string
var TM_ADDR string
var WORKING_DIR string
var FEE_GRANTER_ADDR string

func WithAppName(a string) {
	APP_NAME = a
}

func WithGasPrices(g string) {
	GAS_PRICES = g
}
func WithGitopiaAddr(a string) {
	GITOPIA_ADDR = a
}

func WithChainId(c string) {
	CHAIN_ID = c
}
func WithTmAddr(a string) {
	TM_ADDR = a
}
func WithWorkingDir(w string) {
	WORKING_DIR = w
}
func WithFeeGranter(a string) {
	FEE_GRANTER_ADDR = a
}
