package cmd

import (
	"testing"
	"time"

	eUtils "github.com/apernet/hysteria/extras/v2/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMakeRealmLocalUDPOptions(t *testing.T) {
	t.Run("supports legacy lport", func(t *testing.T) {
		opts, err := makeRealmLocalUDPOptions("", "", 0, 4433)
		require.NoError(t, err)
		assert.Equal(t, eUtils.PortUnion{{Start: 4433, End: 4433}}, opts.Ports)
		assert.Equal(t, realmLocalUDPFamilyAuto, opts.PreferFamily)
	})

	t.Run("parses range and family preference", func(t *testing.T) {
		opts, err := makeRealmLocalUDPOptions("40000-40002,41000", "prefer-v6", 1500*time.Millisecond, 0)
		require.NoError(t, err)
		assert.Equal(t, eUtils.PortUnion{{Start: 40000, End: 40002}, {Start: 41000, End: 41000}}, opts.Ports)
		assert.Equal(t, realmLocalUDPFamilyIPv6, opts.PreferFamily)
		assert.Equal(t, 1500*time.Millisecond, opts.FallbackTimeout)
	})

	t.Run("rejects mixed listenPorts and lport", func(t *testing.T) {
		_, err := makeRealmLocalUDPOptions("40000-40002", "", 0, 4433)
		assert.EqualError(t, err, "listenPorts cannot be used together with legacy lport")
	})

	t.Run("rejects invalid family", func(t *testing.T) {
		_, err := makeRealmLocalUDPOptions("", "ipv10", 0, 0)
		assert.EqualError(t, err, "preferIPVersion must be one of auto, v4, or v6")
	})

	t.Run("rejects invalid port range", func(t *testing.T) {
		_, err := makeRealmLocalUDPOptions("0,40000-40001", "", 0, 0)
		assert.EqualError(t, err, "listenPorts must only contain ports in 1-65535")
	})

	t.Run("rejects negative fallback timeout", func(t *testing.T) {
		_, err := makeRealmLocalUDPOptions("", "", -time.Second, 0)
		assert.EqualError(t, err, "fallbackTimeout must not be negative")
	})
}
