package keeper

import (
	"fmt"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/umee-network/umee/v6/x/metoken"
	otypes "github.com/umee-network/umee/v6/x/oracle/types"
)

var usdExponent = uint32(0)

// Prices calculates meToken price as an avg of median prices of all index accepted assets.
func (k Keeper) Prices(index metoken.Index) (metoken.IndexPrices, error) {
	indexPrices := metoken.EmptyIndexPrices(index)

	supply, err := k.IndexBalances(index.Denom)
	if err != nil {
		return indexPrices, err
	}

	allPrices := k.oracleKeeper.AllMedianPrices(*k.ctx)

	// calculate the total assets value in the index balances
	totalAssetsUSDValue := sdk.ZeroDec()
	for _, aa := range index.AcceptedAssets {
		// get token settings from leverageKeeper to use the symbol_denom
		tokenSettings, err := k.leverageKeeper.GetTokenSettings(*k.ctx, aa.Denom)
		if err != nil {
			return indexPrices, err
		}

		assetPrice, err := latestPrice(allPrices, tokenSettings.SymbolDenom)
		if err != nil {
			return indexPrices, err
		}

		indexPrices.SetPrice(
			metoken.AssetPrice{
				BaseDenom:   aa.Denom,
				SymbolDenom: tokenSettings.SymbolDenom,
				Price:       assetPrice,
				Exponent:    tokenSettings.Exponent,
			},
		)

		// if no meTokens were minted, the totalAssetValue is the sum of all the assets prices.
		// otherwise is the sum of the value of all the assets in the index.
		if supply.MetokenSupply.IsZero() {
			totalAssetsUSDValue = totalAssetsUSDValue.Add(assetPrice)
		} else {
			balance, i := supply.AssetBalance(aa.Denom)
			if i < 0 {
				return indexPrices, sdkerrors.ErrNotFound.Wrapf("balance for denom %s not found", aa.Denom)
			}

			assetUSDValue, err := valueInUSD(balance.AvailableSupply(), assetPrice, tokenSettings.Exponent)
			if err != nil {
				return indexPrices, err
			}
			totalAssetsUSDValue = totalAssetsUSDValue.Add(assetUSDValue)
		}
	}

	if supply.MetokenSupply.IsZero() {
		// if no meTokens were minted, the meTokenPrice is totalAssetsUSDValue divided by accepted assets quantity
		indexPrices.Price = totalAssetsUSDValue.QuoInt(sdkmath.NewInt(int64(len(index.AcceptedAssets))))
	} else {
		// otherwise, the meTokenPrice is totalAssetsUSDValue divided by meTokens minted quantity
		meTokenPrice, err := priceInUSD(supply.MetokenSupply.Amount, totalAssetsUSDValue, index.Exponent)
		if err != nil {
			return indexPrices, err
		}
		indexPrices.Price = meTokenPrice
	}

	for i := 0; i < len(indexPrices.Assets); i++ {
		asset := indexPrices.Assets[i]
		swapRate, err := metoken.Rate(asset.Price, indexPrices.Price, asset.Exponent, indexPrices.Exponent)
		if err != nil {
			return indexPrices, err
		}

		redeemRate, err := metoken.Rate(indexPrices.Price, asset.Price, indexPrices.Exponent, asset.Exponent)
		if err != nil {
			return indexPrices, err
		}

		indexPrices.Assets[i].SwapRate = swapRate
		indexPrices.Assets[i].RedeemRate = redeemRate
	}

	return indexPrices, nil
}

// latestPrice from the list of medians, based on the block number.
func latestPrice(prices otypes.Prices, symbolDenom string) (sdk.Dec, error) {
	latestPrice := otypes.Price{}
	for _, price := range prices {
		if price.ExchangeRateTuple.Denom == symbolDenom && price.BlockNum > latestPrice.BlockNum {
			latestPrice = price
		}
	}

	if latestPrice.BlockNum == 0 {
		return sdk.Dec{}, fmt.Errorf("price not found in oracle for denom %s", symbolDenom)
	}

	return latestPrice.ExchangeRateTuple.ExchangeRate, nil
}

// valueInUSD given a specific amount, price and exponent
func valueInUSD(amount sdkmath.Int, assetPrice sdk.Dec, assetExponent uint32) (sdk.Dec, error) {
	exponentFactor, err := metoken.ExponentFactor(assetExponent, usdExponent)
	if err != nil {
		return sdk.Dec{}, err
	}
	return exponentFactor.MulInt(amount).Mul(assetPrice), nil
}

// priceInUSD given a specific amount, totalValue and exponent
func priceInUSD(amount sdkmath.Int, totalValue sdk.Dec, assetExponent uint32) (sdk.Dec, error) {
	exponentFactor, err := metoken.ExponentFactor(assetExponent, usdExponent)
	if err != nil {
		return sdk.Dec{}, err
	}

	return totalValue.Quo(exponentFactor.MulInt(amount)), nil
}
