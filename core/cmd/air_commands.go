package cmd

import (
	"github.com/urfave/cli/v2"

	"github.com/JelmerDeHen/xidle"
)


/*
# Shows up as 2 pids but they're parent/child
[Service]
ExecStartPre=sh -c "MONIF=$$(ip -j address | jq -r '.[] | select( .link_type == \"ieee802.11/radiotap\" ) | .ifname') && echo >&2 \"MONIF=$${MONIF}\" && [ -z \"$${MONIF}\" ] && WLANIF=$$( airmon-ng | awk '{print $$2}' | grep -v Interface | grep '^[^ ]' | head -n1 ) && echo >&2 \"WLANIF==$${WLANIF}\" && airmon-ng start $$WLANIF || echo >&2 \"Monitor mode was enabled\""
ExecStartPre=-killall airodump-ng
# -K 1 = non-interactive mode
#PIDFile=/run/airodump-ng.pid
#ExecStartPre=echo $PID > /run/airodump-ng.pid
ExecStart=sh -c ' MONIF=$$(ip -j address | jq -r \'.[] | select( .link_type == "ieee802.11/radiotap" ) | .ifname\') && echo "MON=$$MONIF" &&  airodump-ng -K 1 --output-format pcap --band abg -w "/data3/mon/ieee802.11/$$(hostname).$(date "+%%Y%%m%%d.%%H%%M").pcap" wlo1mon'
ExecStop=-killall airodump-ng
Restart=always
RestartSec=3
[Install]
WantedBy=multi-user.target
*/

func (cli *Client) Air(cCtx *cli.Context) error {
	args := []string{
		"-D", "sysdefault:CARD=NTUSB",
		"-t", "wav",
		"-f", "cd",
		//"-f", "S24_3LE",
		//"-r", "41000",
		"-d", "3600",
		"${OUTFILE}",
	}
	job := xidle.NewCmdJob("arecord", args...)

	job.OutfileGenerator = func() string {
		return getOutfilename("/data/mon/air", "pcap")
	}

	idlemon := xidle.NewIdlemon(job)
	idlemon.Run()

	return nil
}
