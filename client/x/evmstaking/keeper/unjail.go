//nolint:contextcheck // use cached context
package keeper

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"
	slashtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	ethtypes "github.com/ethereum/go-ethereum/core/types"

	"github.com/piplabs/story/client/x/evmstaking/types"
	"github.com/piplabs/story/contracts/bindings"
	"github.com/piplabs/story/lib/errors"
	"github.com/piplabs/story/lib/k1util"
)

func (k Keeper) ProcessUnjail(ctx context.Context, ev *bindings.IPTokenStakingUnjail) (err error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cachedCtx, writeCache := sdkCtx.CacheContext()

	defer func() {
		if r := recover(); r != nil {
			err = errors.WrapErrWithCode(errors.UnexpectedCondition, fmt.Errorf("panic caused by %v", r))
		}
		if err == nil {
			writeCache()
			return
		}
		sdkCtx.EventManager().EmitEvents(sdk.Events{
			sdk.NewEvent(
				types.EventTypeUnjailFailure,
				sdk.NewAttribute(types.AttributeKeyBlockHeight, strconv.FormatInt(sdkCtx.BlockHeight(), 10)),
				sdk.NewAttribute(types.AttributeKeyValidatorUncmpPubKey, hex.EncodeToString(ev.ValidatorUncmpPubkey)),
				sdk.NewAttribute(types.AttributeKeySenderAddress, ev.Unjailer.Hex()),
				sdk.NewAttribute(types.AttributeKeyStatusCode, errors.UnwrapErrCode(err).String()),
				sdk.NewAttribute(types.AttributeKeyTxHash, hex.EncodeToString(ev.Raw.TxHash.Bytes())),
			),
		})
	}()

	valCmpPubkey, err := UncmpPubKeyToCmpPubKey(ev.ValidatorUncmpPubkey)
	if err != nil {
		return errors.WrapErrWithCode(errors.InvalidUncmpPubKey, errors.Wrap(err, "compress validator pubkey"))
	}
	validatorPubkey, err := k1util.PubKeyBytesToCosmos(valCmpPubkey)
	if err != nil {
		return errors.Wrap(err, "validator pubkey to cosmos")
	}

	valAddr := sdk.ValAddress(validatorPubkey.Address().Bytes())
	valDelAddr := sdk.AccAddress(validatorPubkey.Address().Bytes())

	valEvmAddr, err := k1util.CosmosPubkeyToEVMAddress(valCmpPubkey)
	if err != nil {
		return errors.Wrap(err, "validator pubkey to evm address")
	}

	// unjailOnBehalf txn, need to check if it's from the operator
	if valEvmAddr.String() != ev.Unjailer.String() {
		operatorAddr, err := k.DelegatorOperatorAddress.Get(cachedCtx, valDelAddr.String())
		if errors.Is(err, collections.ErrNotFound) {
			return errors.WrapErrWithCode(
				errors.InvalidOperator,
				errors.New("invalid unjailOnBehalf txn, no operator for delegator"),
			)
		} else if err != nil {
			return errors.Wrap(err, "get validator's operator address failed")
		}
		if operatorAddr != ev.Unjailer.String() {
			return errors.WrapErrWithCode(
				errors.InvalidOperator,
				errors.New("invalid unjailOnBehalf txn, not from operator"),
			)
		}
	}

	if err = k.slashingKeeper.Unjail(cachedCtx, valAddr); errors.Is(err, slashtypes.ErrNoValidatorForAddress) {
		return errors.WrapErrWithCode(errors.ValidatorNotFound, err)
	} else if errors.Is(err, slashtypes.ErrMissingSelfDelegation) {
		return errors.WrapErrWithCode(errors.MissingSelfDelegation, err)
	} else if errors.Is(err, slashtypes.ErrValidatorNotJailed) {
		return errors.WrapErrWithCode(errors.ValidatorNotJailed, err)
	} else if errors.Is(err, slashtypes.ErrValidatorJailed) {
		return errors.WrapErrWithCode(errors.ValidatorStillJailed, err)
	} else if err != nil {
		return errors.Wrap(err, "unjail")
	}

	return nil
}

func (k Keeper) ParseUnjailLog(ethlog ethtypes.Log) (*bindings.IPTokenStakingUnjail, error) {
	return k.ipTokenStakingContract.ParseUnjail(ethlog)
}
