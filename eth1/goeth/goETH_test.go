package goeth

import (
	"encoding/hex"
	"github.com/bloxapp/ssv/utils/logex"
	"go.uber.org/zap/zapcore"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/stretchr/testify/require"

	"github.com/bloxapp/ssv/eth1"
	"github.com/bloxapp/ssv/shared/params"
)

func TestReadingEventLogs(t *testing.T) {
	t.Run("Successfully Process ValidatorAdded Event", func(t *testing.T) {
		eventData := "000000000000000000000000a5cfd290965372553efd5fdaeb91c335207b76e2000000000000000000000000000000000000000000000000000000000000006000000000000000000000000000000000000000000000000000000000000000c00000000000000000000000000000000000000000000000000000000000000030b2cfa21860b2fdf3b49dbc9189f46e0cd937a129c0d0ead07d5bd0e3d9df83180bd7e57df2226aecc3be4eb397e56fd50000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000040000000000000000000000000000000000000000000000000000000000000080000000000000000000000000000000000000000000000000000000000000020000000000000000000000000000000000000000000000000000000000000003c000000000000000000000000000000000000000000000000000000000000005400000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000008000000000000000000000000000000000000000000000000000000000000000e00000000000000000000000000000000000000000000000000000000000000140000000000000000000000000000000000000000000000000000000000000002a303364663464386633313166316366623364396235396133646531623638666663303665383235363230000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000030b2a8065a2e43975c2fc50fc2e718c311e78e8972ebe7b8d5f903ea3ba7266cccab0890a82b2bd3d1a07cc097c72a75b10000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000201b520ec18ef1591b6d6143ada0c82a60c99d6696b354d890e65afd5bfc30453c0000000000000000000000000000000000000000000000000000000000000001000000000000000000000000000000000000000000000000000000000000008000000000000000000000000000000000000000000000000000000000000001200000000000000000000000000000000000000000000000000000000000000180000000000000000000000000000000000000000000000000000000000000008030626430386533333338633937336534623663393162613135356362373164656536616234313131306465396137633939343163626265326135363533303861383830326663376362323532386438393134643362656563376538653437383465376637616534343935383238303731656266336537366237343863353363370000000000000000000000000000000000000000000000000000000000000030b4ec1015905665cd11ede6f7875ddeb6ba6b03e6f127de668394cf99556da6f93e0c09280ded9b882044ae7c3dbac1d60000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000202df186eb9d5ec2fdf892d897f3268cfca4cc62d25abe7eec0797afa54f9e6cf30000000000000000000000000000000000000000000000000000000000000002000000000000000000000000000000000000000000000000000000000000008000000000000000000000000000000000000000000000000000000000000000e00000000000000000000000000000000000000000000000000000000000000140000000000000000000000000000000000000000000000000000000000000002a307837643664364333313962306445323834316242304535363433303044306445344438423066343033000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000030b4247f3be66a12feccde71e76a6e2aef4e64668bc3db3fc8e80486b105aa5688eff1a731dd3649963b1e53078801f149000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000020589aca2c3ee2c558e3127a0607e7c320c3ebd2f588e74fe76707b3041e196d2200000000000000000000000000000000000000000000000000000000000000030000000000000000000000000000000000000000000000000000000000000080000000000000000000000000000000000000000000000000000000000000012000000000000000000000000000000000000000000000000000000000000001800000000000000000000000000000000000000000000000000000000000000062307838303030313237303864633033663631313735316161643761343361303832313432383332623563316163656564303766663962353433636638333633383138363133353261613932336337306565623032303138623633386161333036616100000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000309029a76319d9bc455f86e7d65fe85cc623c7b68732f7a90cc750553742507158424a17366ab710c4ada729a210692b1e0000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000202760313049dfe2e3f9a64fefd569f4c7d33e12fd3dd0ef8404ab077967a145c8"
		contractAbi, err := abi.JSON(strings.NewReader(params.SsvConfig().ContractABI))
		require.NoError(t, err)
		e := &eth1GRPC{
			ctx:    nil,
			conn:   nil,
			logger: logex.Build("SSV-CLI", zapcore.DebugLevel),
			contractEvent: eth1.NewContractEvent("smartContractEvent"),
		}
		data, err := hex.DecodeString(eventData)
		require.NoError(t, err)
		err = e.ProcessValidatorAddedEvent(data,contractAbi,"ValidatorAdded")
		require.NoError(t, err)
	})
}
