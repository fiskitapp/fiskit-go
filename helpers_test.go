package fiskit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
)

func strconvItoa(n int64) string { return strconv.FormatInt(n, 10) }

func sign(secret, id string, timestamp int64, body []byte) string {
	key, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + strconvItoa(timestamp) + "."))
	mac.Write(body)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
