// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package p

import (
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/api/info"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/tests/fixture/e2e"
	"github.com/ava-labs/avalanchego/utils"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/crypto/bls"
	"github.com/ava-labs/avalanchego/utils/units"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"github.com/ava-labs/avalanchego/vms/platformvm/signer"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"

	ginkgo "github.com/onsi/ginkgo/v2"
)

// PChainWorkflow is an integration test for normal P-Chain operations
// - Issues an Add Validator and an Add Delegator using the funding address
// - Exports AVAX from the P-Chain funding address to the X-Chain created address
// - Exports AVAX from the X-Chain created address to the P-Chain created address
// - Checks the expected value of the funding address

var _ = e2e.DescribePChain("[Workflow]", func() {
	tc := e2e.NewTestContext()
	require := require.New(tc)

	ginkgo.It("P-chain main operations",
		func() {
			env := e2e.GetEnv(tc)

			nodeURI := env.GetRandomNodeURI()
			keychain := env.NewKeychain(2)
			baseWallet := e2e.NewWallet(tc, keychain, nodeURI)

			pWallet := baseWallet.P()
			pBuilder := pWallet.Builder()
			pContext := pBuilder.Context()
			avaxAssetID := pContext.AVAXAssetID
			xWallet := baseWallet.X()
			xBuilder := xWallet.Builder()
			xContext := xBuilder.Context()
			pChainClient := platformvm.NewClient(nodeURI.URI)

			tc.Outf("{{blue}} fetching minimal stake amounts {{/}}\n")
			minValStake, minDelStake, err := pChainClient.GetMinStake(tc.DefaultContext(), constants.PlatformChainID)
			require.NoError(err)
			tc.Outf("{{green}} minimal validator stake: %d {{/}}\n", minValStake)
			tc.Outf("{{green}} minimal delegator stake: %d {{/}}\n", minDelStake)

			tc.Outf("{{blue}} fetching tx fee {{/}}\n")
			infoClient := info.NewClient(nodeURI.URI)
			fees, err := infoClient.GetTxFee(tc.DefaultContext())
			require.NoError(err)
			txFees := uint64(fees.TxFee)
			tc.Outf("{{green}} txFee: %d {{/}}\n", txFees)

			// amount to transfer from P to X chain
			toTransfer := 1 * units.Avax

			pShortAddr := keychain.Keys[0].Address()
			xTargetAddr := keychain.Keys[1].Address()
			tc.By("check selected keys have sufficient funds", func() {
				pBalances, err := pWallet.Builder().GetBalance()
				pBalance := pBalances[avaxAssetID]
				minBalance := minValStake + txFees + minDelStake + txFees + toTransfer + txFees
				require.NoError(err)
				require.GreaterOrEqual(pBalance, minBalance)
			})

			// Use a random node ID to ensure that repeated test runs
			// will succeed against a network that persists across runs.
			validatorID, err := ids.ToNodeID(utils.RandomBytes(ids.NodeIDLen))
			require.NoError(err)

			vdr := &txs.SubnetValidator{
				Validator: txs.Validator{
					NodeID: validatorID,
					End:    uint64(time.Now().Add(72 * time.Hour).Unix()),
					Wght:   minValStake,
				},
				Subnet: constants.PrimaryNetworkID,
			}
			rewardOwner := &secp256k1fx.OutputOwners{
				Threshold: 1,
				Addrs:     []ids.ShortID{pShortAddr},
			}
			shares := uint32(20000) // TODO: retrieve programmatically

			sk, err := bls.NewSecretKey()
			require.NoError(err)
			pop := signer.NewProofOfPossession(sk)

			tc.By("issue add validator tx", func() {
				_, err := pWallet.IssueAddPermissionlessValidatorTx(
					vdr,
					pop,
					avaxAssetID,
					rewardOwner,
					rewardOwner,
					shares,
					tc.WithDefaultContext(),
				)
				require.NoError(err)
			})

			tc.By("issue add delegator tx", func() {
				_, err := pWallet.IssueAddPermissionlessDelegatorTx(
					vdr,
					avaxAssetID,
					rewardOwner,
					tc.WithDefaultContext(),
				)
				require.NoError(err)
			})

			// retrieve initial balances
			pBalances, err := pWallet.Builder().GetBalance()
			require.NoError(err)
			pStartBalance := pBalances[avaxAssetID]
			tc.Outf("{{blue}} P-chain balance before P->X export: %d {{/}}\n", pStartBalance)

			xBalances, err := xWallet.Builder().GetFTBalance()
			require.NoError(err)
			xStartBalance := xBalances[avaxAssetID]
			tc.Outf("{{blue}} X-chain balance before P->X export: %d {{/}}\n", xStartBalance)

			outputOwner := secp256k1fx.OutputOwners{
				Threshold: 1,
				Addrs: []ids.ShortID{
					xTargetAddr,
				},
			}
			output := &secp256k1fx.TransferOutput{
				Amt:          toTransfer,
				OutputOwners: outputOwner,
			}

			tc.By("export avax from P to X chain", func() {
				_, err := pWallet.IssueExportTx(
					xContext.BlockchainID,
					[]*avax.TransferableOutput{
						{
							Asset: avax.Asset{
								ID: avaxAssetID,
							},
							Out: output,
						},
					},
					tc.WithDefaultContext(),
				)
				require.NoError(err)
			})

			// check balances post export
			pBalances, err = pWallet.Builder().GetBalance()
			require.NoError(err)
			pPreImportBalance := pBalances[avaxAssetID]
			tc.Outf("{{blue}} P-chain balance after P->X export: %d {{/}}\n", pPreImportBalance)

			xBalances, err = xWallet.Builder().GetFTBalance()
			require.NoError(err)
			xPreImportBalance := xBalances[avaxAssetID]
			tc.Outf("{{blue}} X-chain balance after P->X export: %d {{/}}\n", xPreImportBalance)

			require.Equal(xPreImportBalance, xStartBalance) // import not performed yet
			require.Equal(pPreImportBalance, pStartBalance-toTransfer-txFees)

			tc.By("import avax from P into X chain", func() {
				_, err := xWallet.IssueImportTx(
					constants.PlatformChainID,
					&outputOwner,
					tc.WithDefaultContext(),
				)
				require.NoError(err)
			})

			// check balances post import
			pBalances, err = pWallet.Builder().GetBalance()
			require.NoError(err)
			pFinalBalance := pBalances[avaxAssetID]
			tc.Outf("{{blue}} P-chain balance after P->X import: %d {{/}}\n", pFinalBalance)

			xBalances, err = xWallet.Builder().GetFTBalance()
			require.NoError(err)
			xFinalBalance := xBalances[avaxAssetID]
			tc.Outf("{{blue}} X-chain balance after P->X import: %d {{/}}\n", xFinalBalance)

			require.Equal(xFinalBalance, xPreImportBalance+toTransfer-txFees) // import not performed yet
			require.Equal(pFinalBalance, pPreImportBalance)
		})
})
