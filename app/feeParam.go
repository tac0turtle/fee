package app

import (
	"fmt"

	"github.com/cosmos/cosmos-sdk/baseapp"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/x/params/types"
)

var ParamStoreKeyfee = []byte("fee")

type Fee struct {
	Fee sdk.DecCoins
}

func NewFeeparam(fee sdk.DecCoins) Fee {
	return Fee{Fee: fee}
}

var feeParamSet = types.NewParamSetPair(
	ParamStoreKeyfee, Fee{}, ValidateFee,
)

func ValidateFee(i interface{}) error {
	v, ok := i.(Fee)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}

	if v.Fee.Empty() {
		return fmt.Errorf("fee must be positive: %s", v.Fee.String())
	}

	return nil
}

//////

// FeeParamDecorator will check if the transaction's fee is at least as large
// as the local validator's minimum gasFee (defined in validator config).
// If fee is too low, decorator returns error and tx is rejected from mempool.
// Note this only applies when ctx.CheckTx = true
// If fee is high enough or not CheckTx, then call next AnteHandler
// CONTRACT: Tx must implement FeeTx to use FeeParamDecorator
type FeeParamDecorator struct {
	ParamStore baseapp.ParamStore
}

func NewFeeParamDecorator(params baseapp.ParamStore) FeeParamDecorator {
	return FeeParamDecorator{
		ParamStore: params,
	}
}

func (mfd FeeParamDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (newCtx sdk.Context, err error) {
	feeTx, ok := tx.(sdk.FeeTx)
	if !ok {
		return ctx, sdkerrors.Wrap(sdkerrors.ErrTxDecode, "Tx must be a FeeTx")
	}

	feeCoins := feeTx.GetFee()
	gas := feeTx.GetGas()

	// Ensure that the provided fees meet a minimum threshold for the validator,
	// if this is a CheckTx. This is only for local mempool purposes, and thus
	// is only ran on check tx.
	if ctx.IsCheckTx() && !simulate {
		var minGasPrices sdk.DecCoins
		mfd.ParamStore.Get(ctx, ParamStoreKeyfee, minGasPrices)
		if !minGasPrices.IsZero() {
			requiredFees := make(sdk.Coins, len(minGasPrices))

			// Determine the required fees by multiplying each required minimum gas
			// price by the gas limit, where fee = ceil(minGasPrice * gasLimit).
			glDec := sdk.NewDec(int64(gas))
			for i, gp := range minGasPrices {
				fee := gp.Amount.Mul(glDec)
				requiredFees[i] = sdk.NewCoin(gp.Denom, fee.Ceil().RoundInt())
			}

			if !feeCoins.IsAnyGTE(requiredFees) {
				return ctx, sdkerrors.Wrapf(sdkerrors.ErrInsufficientFee, "insufficient fees; got: %s required: %s", feeCoins, requiredFees)
			}
		}
	}

	return next(ctx, tx, simulate)
}
