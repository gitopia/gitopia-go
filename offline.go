package gitopia

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/gitopia/gitopia/v5/x/offchain/types"
	"github.com/pkg/errors"
)

func (c Client) SignMsg(data []byte) ([]byte, error) {
	signerAddr := c.Address()
	if signerAddr == nil {
		return nil, errors.New("error getting signer address")
	}

	msg := types.NewMsgSignData(signerAddr, data)
	signer, err := types.NewSignerFromClientContext(c.cc)
	if err != nil {
		return nil, errors.Wrap(err, "error while creating the signer object")
	}

	txObj, err := signer.Sign([]sdk.Msg{msg})
	if err != nil {
		return nil, errors.WithMessage(err, "error signing msg")
	}

	txData, err := c.cc.TxConfig.TxJSONEncoder()(txObj)
	if err != nil {
		return nil, errors.WithMessage(err, "error encoding tx")
	}

	return txData, nil
}

func (c Client) VerifyMsg(data []byte) ([]byte, string, error) {
	txObj, err := c.cc.TxConfig.TxDecoder()(data)
	if err != nil {
		return nil, "", errors.WithMessage(err, "error decoding tx obj")
	}

	verifier := types.NewVerifier(c.cc.TxConfig.SignModeHandler())
	err = verifier.Verify(txObj)
	if err != nil {
		return nil, "", errors.WithMessage(err, "error verifying msg")
	}

	if len(txObj.GetMsgs()) != 1 {
		return nil, "", errors.New("required exactly one signed message")
	}

	msgSign, ok := txObj.GetMsgs()[0].(*types.MsgSignData)
	if !ok {
		return nil, "", errors.New("signed msg not found")
	}

	memoTx, ok := txObj.(sdk.TxWithMemo)
	if !ok {
		return nil, "", errors.New("memo not found")
	}

	return []byte(memoTx.GetMemo()), msgSign.GetSigner(), nil
}
