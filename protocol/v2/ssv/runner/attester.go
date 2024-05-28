package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	specqbft "github.com/bloxapp/ssv-spec/qbft"
	specssv "github.com/bloxapp/ssv-spec/ssv"
	spectypes "github.com/bloxapp/ssv-spec/types"
	ssz "github.com/ferranbt/fastssz"
	"github.com/pkg/errors"
	"github.com/prysmaticlabs/go-bitfield"
	"go.uber.org/zap"

	"github.com/bloxapp/ssv/logging/fields"
	"github.com/bloxapp/ssv/protocol/v2/qbft/controller"
	"github.com/bloxapp/ssv/protocol/v2/ssv/runner/metrics"
)

type AttestationDataCache struct {
	data map[phase0.Slot]*CacheEntry
	mu   sync.Mutex
}

type CacheEntry struct {
	Data     *phase0.AttestationData
	Ready    chan struct{} // closed when data is fetched
	Fetching bool          // indicates if the fetch is in progress
}

func NewAttestationDataCache() *AttestationDataCache {
    cache := &AttestationDataCache{
        data: make(map[phase0.Slot]*CacheEntry),
    }
    go cache.cleanupRoutine()
    return cache
}

func (c *AttestationDataCache) cleanupRoutine() {
    ticker := time.NewTicker(10 * time.Minute)
    for {
        <-ticker.C
        c.cleanup()
    }
}

func (c *AttestationDataCache) cleanup() {
    c.mu.Lock()
    defer c.mu.Unlock()

    // Find the maximum slot key
    var maxSlot phase0.Slot
    for slot := range c.data {
        if slot > maxSlot {
            maxSlot = slot
        }
    }

    // Remove all entries except for recent ones (age >10 slot)
    for slot := range c.data {
        if slot < (maxSlot - 10) {
            delete(c.data, slot)
        }
    }
}

// GetOrCreateEntry gets or creates a cache entry for the slot.
func (c *AttestationDataCache) GetOrCreateEntry(slot phase0.Slot) *CacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, exists := c.data[slot]; exists {
		return entry
	}

	// Create a new entry if one does not exist
	entry := &CacheEntry{
		Ready: make(chan struct{}),
	}
	c.data[slot] = entry
	return entry
}

type AttesterRunner struct {
	BaseRunner *BaseRunner

	beacon   specssv.BeaconNode
	network  specssv.Network
	signer   spectypes.KeyManager
	valCheck specqbft.ProposedValueCheckF

	started time.Time
	metrics metrics.ConsensusMetrics

	attDataCache *AttestationDataCache
}

func NewAttesterRunnner(
	beaconNetwork spectypes.BeaconNetwork,
	share *spectypes.Share,
	qbftController *controller.Controller,
	beacon specssv.BeaconNode,
	network specssv.Network,
	signer spectypes.KeyManager,
	valCheck specqbft.ProposedValueCheckF,
	highestDecidedSlot phase0.Slot,
	attDataCache *AttestationDataCache,
) Runner {
	return &AttesterRunner{
		BaseRunner: &BaseRunner{
			BeaconRoleType:     spectypes.BNRoleAttester,
			BeaconNetwork:      beaconNetwork,
			Share:              share,
			QBFTController:     qbftController,
			highestDecidedSlot: highestDecidedSlot,
		},

		beacon:   beacon,
		network:  network,
		signer:   signer,
		valCheck: valCheck,

		metrics: metrics.NewConsensusMetrics(spectypes.BNRoleAttester),

		attDataCache: attDataCache,
	}
}

func (r *AttesterRunner) StartNewDuty(logger *zap.Logger, duty *spectypes.Duty) error {
	return r.BaseRunner.baseStartNewDuty(logger, r, duty)
}

// HasRunningDuty returns true if a duty is already running (StartNewDuty called and returned nil)
func (r *AttesterRunner) HasRunningDuty() bool {
	return r.BaseRunner.hasRunningDuty()
}

func (r *AttesterRunner) ProcessPreConsensus(logger *zap.Logger, signedMsg *spectypes.SignedPartialSignatureMessage) error {
	return errors.New("no pre consensus sigs required for attester role")
}

func (r *AttesterRunner) ProcessConsensus(logger *zap.Logger, signedMsg *specqbft.SignedMessage) error {
	decided, decidedValue, err := r.BaseRunner.baseConsensusMsgProcessing(logger, r, signedMsg)
	if err != nil {
		return errors.Wrap(err, "failed processing consensus message")
	}

	// Decided returns true only once so if it is true it must be for the current running instance
	if !decided {
		return nil
	}

	r.metrics.EndConsensus()
	r.metrics.StartPostConsensus()

	attestationData, err := decidedValue.GetAttestationData()
	if err != nil {
		return errors.Wrap(err, "could not get attestation data")
	}

	// specific duty sig
	msg, err := r.BaseRunner.signBeaconObject(r, attestationData, decidedValue.Duty.Slot, spectypes.DomainAttester)
	if err != nil {
		return errors.Wrap(err, "failed signing attestation data")
	}
	postConsensusMsg := &spectypes.PartialSignatureMessages{
		Type:     spectypes.PostConsensusPartialSig,
		Slot:     decidedValue.Duty.Slot,
		Messages: []*spectypes.PartialSignatureMessage{msg},
	}

	postSignedMsg, err := r.BaseRunner.signPostConsensusMsg(r, postConsensusMsg)
	if err != nil {
		return errors.Wrap(err, "could not sign post consensus msg")
	}

	data, err := postSignedMsg.Encode()
	if err != nil {
		return errors.Wrap(err, "failed to encode post consensus signature msg")
	}

	msgToBroadcast := &spectypes.SSVMessage{
		MsgType: spectypes.SSVPartialSignatureMsgType,
		MsgID:   spectypes.NewMsgID(r.GetShare().DomainType, r.GetShare().ValidatorPubKey, r.BaseRunner.BeaconRoleType),
		Data:    data,
	}

	if err := r.GetNetwork().Broadcast(msgToBroadcast); err != nil {
		return errors.Wrap(err, "can't broadcast partial post consensus sig")
	}
	return nil
}

func (r *AttesterRunner) ProcessPostConsensus(logger *zap.Logger, signedMsg *spectypes.SignedPartialSignatureMessage) error {
	quorum, roots, err := r.BaseRunner.basePostConsensusMsgProcessing(logger, r, signedMsg)
	if err != nil {
		return errors.Wrap(err, "failed processing post consensus message")
	}

	duty := r.GetState().DecidedValue.Duty
	logger = logger.With(fields.Slot(duty.Slot))
	logger.Debug("🧩 got partial signatures",
		zap.Uint64("signer", signedMsg.Signer))

	if !quorum {
		return nil
	}

	r.metrics.EndPostConsensus()

	attestationData, err := r.GetState().DecidedValue.GetAttestationData()
	if err != nil {
		return errors.Wrap(err, "could not get attestation data")
	}

	for _, root := range roots {
		sig, err := r.GetState().ReconstructBeaconSig(r.GetState().PostConsensusContainer, root, r.GetShare().ValidatorPubKey)
		if err != nil {
			// If the reconstructed signature verification failed, fall back to verifying each partial signature
			for _, root := range roots {
				r.BaseRunner.FallBackAndVerifyEachSignature(r.GetState().PostConsensusContainer, root)
			}
			return errors.Wrap(err, "got post-consensus quorum but it has invalid signatures")
		}
		specSig := phase0.BLSSignature{}
		copy(specSig[:], sig)

		logger.Debug("🧩 reconstructed partial signatures",
			zap.Uint64s("signers", getPostConsensusSigners(r.GetState(), root)))

		aggregationBitfield := bitfield.NewBitlist(r.GetState().DecidedValue.Duty.CommitteeLength)
		aggregationBitfield.SetBitAt(duty.ValidatorCommitteeIndex, true)
		signedAtt := &phase0.Attestation{
			Data:            attestationData,
			Signature:       specSig,
			AggregationBits: aggregationBitfield,
		}

		attestationSubmissionEnd := r.metrics.StartBeaconSubmission()
		consensusDuration := time.Since(r.started)

		// Submit it to the BN.
		start := time.Now()
		if err := r.beacon.SubmitAttestation(signedAtt); err != nil {
			r.metrics.RoleSubmissionFailed()
			logger.Error("❌ failed to submit attestation", zap.Error(err))
			return errors.Wrap(err, "could not submit to Beacon chain reconstructed attestation")
		}

		attestationSubmissionEnd()
		r.metrics.EndDutyFullFlow(r.GetState().RunningInstance.State.Round)
		r.metrics.RoleSubmitted()

		logger.Info("✅ successfully submitted attestation",
			zap.String("block_root", hex.EncodeToString(signedAtt.Data.BeaconBlockRoot[:])),
			fields.ConsensusTime(consensusDuration),
			fields.SubmissionTime(time.Since(start)),
			fields.Height(r.BaseRunner.QBFTController.Height),
			fields.Round(r.GetState().RunningInstance.State.Round))
	}
	r.GetState().Finished = true

	return nil
}

func (r *AttesterRunner) expectedPreConsensusRootsAndDomain() ([]ssz.HashRoot, phase0.DomainType, error) {
	return []ssz.HashRoot{}, spectypes.DomainError, errors.New("no expected pre consensus roots for attester")
}

// expectedPostConsensusRootsAndDomain an INTERNAL function, returns the expected post-consensus roots to sign
func (r *AttesterRunner) expectedPostConsensusRootsAndDomain() ([]ssz.HashRoot, phase0.DomainType, error) {
	attestationData, err := r.GetState().DecidedValue.GetAttestationData()
	if err != nil {
		return nil, phase0.DomainType{}, errors.Wrap(err, "could not get attestation data")
	}

	return []ssz.HashRoot{attestationData}, spectypes.DomainAttester, nil
}

// executeDuty steps:
// 1) get attestation data from BN
// 2) start consensus on duty + attestation data
// 3) Once consensus decides, sign partial attestation and broadcast
// 4) collect 2f+1 partial sigs, reconstruct and broadcast valid attestation sig to the BN
func (r *AttesterRunner) executeDuty(logger *zap.Logger, duty *spectypes.Duty) error {
	start := time.Now()

	// Get or create cache entry
	cacheEntry := r.attDataCache.GetOrCreateEntry(duty.Slot)

	var attData *phase0.AttestationData
	var err error

	// Check if the data is already being fetched
	select {
	case <-cacheEntry.Ready:
		// Data is ready, use it
		attData = cacheEntry.Data
		if attData == nil {
			return errors.New("failed to get attestation data from cache")
		}
		logger.Info("Cache hit: using cached attestation data", zap.Uint64("slot", uint64(duty.Slot)))
	default:
		if !cacheEntry.Fetching {
			// Mark as fetching and fetch the data
			cacheEntry.Fetching = true
			logger.Info("Cache miss: fetching attestation data from beacon node", zap.Uint64("slot", uint64(duty.Slot)))
			go func() {
				defer close(cacheEntry.Ready)
				var marshaler ssz.Marshaler
				marshaler, _, err = r.GetBeaconNode().GetAttestationData(duty.Slot, duty.CommitteeIndex)
				if err != nil {
					logger.Error("Failed to fetch attestation data", zap.Error(err))
					return
				}

				retrievedAttData, ok := marshaler.(*phase0.AttestationData)
				if !ok {
					logger.Error("Unexpected type for attestation data")
					return
				}

				// Store in cache
				cacheEntry.Data = retrievedAttData
			}()
			logger.Info("Cache miss: done fetching attestation data from beacon node", zap.Uint64("slot", uint64(duty.Slot)))
			<-cacheEntry.Ready // Wait for the data to be fetched
			attData = cacheEntry.Data
			attData.Index = duty.CommitteeIndex
			if attData == nil {
				return errors.New("failed to get attestation data after fetching")
			}
		} else {
			logger.Info("Cache miss: waiting for ongoing fetch to complete", zap.Uint64("slot", uint64(duty.Slot)))
			<-cacheEntry.Ready // Wait if another goroutine is fetching
			logger.Info("Cache miss: ongoing fetch complete, continuing", zap.Uint64("slot", uint64(duty.Slot)))
			attData = cacheEntry.Data
			attData.Index = duty.CommitteeIndex
			if attData == nil {
				return errors.New("failed to get attestation data from cache after waiting")
			}
		}
	}

	logger = logger.With(zap.Duration("attestation_data_time", time.Since(start)))

	r.started = time.Now()

	r.metrics.StartDutyFullFlow()
	r.metrics.StartConsensus()

	attDataByts, err := attData.MarshalSSZ()
	if err != nil {
		return errors.Wrap(err, "could not marshal attestation data")
	}

	input := &spectypes.ConsensusData{
		Duty:    *duty,
		Version: spec.DataVersionPhase0,
		DataSSZ: attDataByts,
	}

	if err := r.BaseRunner.decide(logger, r, input); err != nil {
		return errors.Wrap(err, "can't start new duty runner instance for duty")
	}
	return nil
}

func (r *AttesterRunner) GetBaseRunner() *BaseRunner {
	return r.BaseRunner
}

func (r *AttesterRunner) GetNetwork() specssv.Network {
	return r.network
}

func (r *AttesterRunner) GetBeaconNode() specssv.BeaconNode {
	return r.beacon
}

func (r *AttesterRunner) GetShare() *spectypes.Share {
	return r.BaseRunner.Share
}

func (r *AttesterRunner) GetState() *State {
	return r.BaseRunner.State
}

func (r *AttesterRunner) GetValCheckF() specqbft.ProposedValueCheckF {
	return r.valCheck
}

func (r *AttesterRunner) GetSigner() spectypes.KeyManager {
	return r.signer
}

// Encode returns the encoded struct in bytes or error
func (r *AttesterRunner) Encode() ([]byte, error) {
	return json.Marshal(r)
}

// Decode returns error if decoding failed
func (r *AttesterRunner) Decode(data []byte) error {
	return json.Unmarshal(data, &r)
}

// GetRoot returns the root used for signing and verification
func (r *AttesterRunner) GetRoot() ([32]byte, error) {
	marshaledRoot, err := r.Encode()
	if err != nil {
		return [32]byte{}, errors.Wrap(err, "could not encode DutyRunnerState")
	}
	ret := sha256.Sum256(marshaledRoot)
	return ret, nil
}
