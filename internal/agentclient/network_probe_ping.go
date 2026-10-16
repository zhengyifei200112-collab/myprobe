package agentclient

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	check "github.com/zhengyifei200112-collab/myprobe/internal/protocol/networkcheck"
	"golang.org/x/text/encoding/simplifiedchinese"
)

var pingCounts = regexp.MustCompile(`(?m)^\s*(\d+) packets transmitted,\s*(\d+)(?: packets)? received,`)
var pingMean = regexp.MustCompile(`(?m)(?:rtt|round-trip)[^=\r\n]*=\s*[0-9.]+/([0-9.]+)/[0-9.]+/`)
var windowsCounts = regexp.MustCompile(`(?mi)(?:Sent|已发送)\s*=\s*(\d+)[^\r\n]*?(?:Received|已接收)\s*=\s*(\d+)[^\r\n]*?(?:Lost|丢失)\s*=\s*(\d+)`)
var windowsMean = regexp.MustCompile(`(?mi)(?:Average|平均)\s*=\s*([0-9.]+)\s*ms`)
var pingReply = regexp.MustCompile(`(?mi)^(?:[^\r\n]*bytes from|Reply from|来自)[^\r\n]*(?:time|时间)\s*[=<]\s*[0-9.]+\s*ms`)

type pingEvidence struct {
	packets     *check.PacketCounts
	transmitted int
	echoReplies int
	rtt         *float64
}

func networkToolText(raw []byte, goos string) string {
	if goos == "windows" && !utf8.Valid(raw) {
		if decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(raw); err == nil {
			return string(decoded)
		}
	}
	return string(raw)
}

func parsePingEvidence(text, goos string, requested int) pingEvidence {
	result := pingEvidence{transmitted: -1, echoReplies: len(pingReply.FindAllString(text, check.MaxPackets+1))}
	pattern, mean := pingCounts, pingMean
	if goos == "windows" {
		pattern, mean = windowsCounts, windowsMean
	}
	counts := pattern.FindAllStringSubmatch(text, 2)
	if len(counts) == 1 {
		sent, sentErr := strconv.Atoi(counts[0][1])
		received, receivedErr := strconv.Atoi(counts[0][2])
		if sentErr == nil && receivedErr == nil && sent >= 0 && sent <= requested && received >= 0 && received <= sent {
			result.transmitted = sent
			valid := true
			if goos == "windows" {
				lost, err := strconv.Atoi(counts[0][3])
				// Windows can count Destination Unreachable as received despite
				// there being no Echo Reply. Require independent reply evidence.
				valid = err == nil && lost == sent-received && received == result.echoReplies
			}
			if valid {
				result.packets = &check.PacketCounts{Sent: sent, Received: received}
			}
		}
	}
	values := mean.FindAllStringSubmatch(text, 2)
	if len(values) == 1 && result.echoReplies > 0 {
		result.rtt = finiteRTT(values[0][1])
	}
	return result
}

func unsupportedPingText(text string) bool {
	lower := strings.ToLower(text)
	for _, value := range []string{"operation not permitted", "permission denied", "address family not supported", "protocol not supported"} {
		if strings.Contains(lower, value) {
			return true
		}
	}
	return false
}
