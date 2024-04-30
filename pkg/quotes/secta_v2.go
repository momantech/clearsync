package quotes

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ipfs/go-log/v2"

	"github.com/layer-3/clearsync/pkg/abi/isecta_v2_factory"
	"github.com/layer-3/clearsync/pkg/abi/isecta_v2_pair"
	"github.com/layer-3/clearsync/pkg/safe"
)

var (
	loggerSectaV2 = log.Logger("secta_v2")
)

type sectaV2 struct {
	factoryAddress common.Address
	factory        *isecta_v2_factory.ISectaV2Factory

	assets *safe.Map[string, poolToken]
	client *ethclient.Client
}

func newSectaV2(config SectaV2Config, outbox chan<- TradeEvent) Driver {
	hooks := &sectaV2{
		factoryAddress: common.HexToAddress(config.FactoryAddress),
	}

	params := baseDexConfig[isecta_v2_pair.ISectaV2PairSwap, isecta_v2_pair.ISectaV2Pair]{
		DriverType:    DriverSectaV2,
		URL:           config.URL,
		AssetsURL:     config.AssetsURL,
		MappingURL:    config.MappingURL,
		Logger:        loggerSectaV2,
		PostStartHook: hooks.postStart,
		PoolGetter:    hooks.getPool,
		EventParser:   hooks.parseSwap,
		Outbox:        outbox,
		Filter:        config.Filter,
	}
	return newBaseDEX(params)
}

func (s *sectaV2) postStart(driver *baseDEX[isecta_v2_pair.ISectaV2PairSwap, isecta_v2_pair.ISectaV2Pair]) (err error) {
	s.client = driver.Client()
	s.assets = driver.Assets()

	s.factory, err = isecta_v2_factory.NewISectaV2Factory(s.factoryAddress, s.client)
	if err != nil {
		return fmt.Errorf("failed to instantiate a Secta v2 pool factory contract: %w", err)
	}
	return nil
}

func (s *sectaV2) getPool(market Market) ([]*dexPool[isecta_v2_pair.ISectaV2PairSwap], error) {
	baseToken, quoteToken, err := getTokens(s.assets, market, loggerSectaV2)
	if err != nil {
		return nil, fmt.Errorf("failed to get tokens: %w", err)
	}

	poolAddress, err := s.factory.GetPair(nil, baseToken.Address, quoteToken.Address)
	if err != nil {
		return nil, fmt.Errorf("failed to get pool address: %w", err)
	}

	zeroAddress := common.HexToAddress("0x0")
	if poolAddress == zeroAddress {
		return nil, fmt.Errorf("pool for market %s does not exist", market.StringWithoutMain())
	}
	loggerSectaV2.Infow("pool found", "market", market.StringWithoutMain(), "address", poolAddress)

	poolContract, err := isecta_v2_pair.NewISectaV2Pair(poolAddress, s.client)
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate a Secta v2 pool contract: %w", err)
	}

	basePoolToken, err := poolContract.Token0(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build Secta v2 pool: %w", err)
	}

	quotePoolToken, err := poolContract.Token1(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build Secta v2 pool: %w", err)
	}

	isReverted := quoteToken.Address == basePoolToken && baseToken.Address == quotePoolToken
	pools := []*dexPool[isecta_v2_pair.ISectaV2PairSwap]{{
		contract:   poolContract,
		baseToken:  baseToken,
		quoteToken: quoteToken,
		reverted:   isReverted,
		market:     market,
	}}

	// Return pools if the token addresses match direct or reversed configurations
	if (baseToken.Address == basePoolToken && quoteToken.Address == quotePoolToken) || isReverted {
		return pools, nil
	}
	return nil, fmt.Errorf("failed to build Secta v2 pool for market %s: %w", market, err)
}

func (s *sectaV2) parseSwap(
	swap *isecta_v2_pair.ISectaV2PairSwap,
	pool *dexPool[isecta_v2_pair.ISectaV2PairSwap],
) (trade TradeEvent, err error) {
	defer func() {
		if r := recover(); r != nil {
			msg := "recovered in from panic during swap parsing"
			loggerSectaV2.Errorw(msg, "swap", swap, "pool", pool)
			err = fmt.Errorf("%s: %s", msg, r)
		}
	}()

	return buildV2Trade(
		DriverSectaV2,
		swap.Amount0In,
		swap.Amount0Out,
		swap.Amount1In,
		swap.Amount1Out,
		pool,
	)
}
