package app

import (
	"fmt"

	"github.com/cosmos/cosmos-sdk/baseapp"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/x/auth/ante"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/params/types"
)

var (
	ParamStoreKeyfee  = []byte("fee")
	ParamStoreKeyburn = []byte("burn")
)

const FeeParamspace = "fee"

type FeeParams struct {
	Fee        sdk.DecCoins
	BurnAmount sdk.Int
}

func NewFeeparam(fee sdk.DecCoins, burnAmount sdk.Int) FeeParams {
	return FeeParams{
		Fee:        fee,
		BurnAmount: burnAmount,
	}
}

func feeParamSet() types.KeyTable {
	return types.NewKeyTable(
		types.NewParamSetPair(
			ParamStoreKeyfee, FeeParams{}, ValidateFee,
		),
	)
}

func ValidateFee(i interface{}) error {
	v, ok := i.(FeeParams)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}

	if v.Fee.Empty() {
		return fmt.Errorf("fee must be positive: %s", v.Fee.String())
	}

	if !v.BurnAmount.GTE(sdk.NewInt(0)) {
		return fmt.Errorf("burn amount must positive: %s ", v.BurnAmount.String())
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

// DeductFeeDecorator deducts fees from the first signer of the tx
// If the first signer does not have the funds to pay for the fees, return with InsufficientFunds error
// Call next AnteHandler if fees successfully deducted
// CONTRACT: Tx must implement FeeTx interface to use DeductFeeDecorator
type DeductFeeDecorator struct {
	ak         ante.AccountKeeper
	bankKeeper authtypes.BankKeeper
	ParamStore baseapp.ParamStore
}

func NewDeductFeeDecorator(ak ante.AccountKeeper, bk authtypes.BankKeeper, params baseapp.ParamStore) DeductFeeDecorator {
	return DeductFeeDecorator{
		ak:         ak,
		bankKeeper: bk,
	}
}

func (dfd DeductFeeDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (newCtx sdk.Context, err error) {
	feeTx, ok := tx.(sdk.FeeTx)
	if !ok {
		return ctx, sdkerrors.Wrap(sdkerrors.ErrTxDecode, "Tx must be a FeeTx")
	}

	if addr := dfd.ak.GetModuleAddress(authtypes.FeeCollectorName); addr == nil {
		panic(fmt.Sprintf("%s module account has not been set", authtypes.FeeCollectorName))
	}

	feePayer := feeTx.FeePayer()
	feePayerAcc := dfd.ak.GetAccount(ctx, feePayer)

	if feePayerAcc == nil {
		return ctx, sdkerrors.Wrapf(sdkerrors.ErrUnknownAddress, "fee payer address: %s does not exist", feePayer)
	}

	// deduct the fees
	if !feeTx.GetFee().IsZero() {
		err = DeductFees(dfd.bankKeeper, ctx, feePayerAcc, feeTx.GetFee())
		if err != nil {
			return ctx, err
		}
	}

	return next(ctx, tx, simulate)
}

// DeductFees deducts fees from the given account.
func DeductFees(bankKeeper authtypes.BankKeeper, ctx sdk.Context, acc authtypes.AccountI, fees sdk.Coins) error {
	if !fees.IsValid() {
		return sdkerrors.Wrapf(sdkerrors.ErrInsufficientFee, "invalid fee amount: %s", fees)
	}

	err := bankKeeper.SendCoinsFromAccountToModule(ctx, acc.GetAddress(), authtypes.FeeCollectorName, fees)
	if err != nil {
		return sdkerrors.Wrapf(sdkerrors.ErrInsufficientFunds, err.Error())
	}

	return nil
}
