package mail

import "strings"

// The links carry the token in the URL fragment, which browsers never send to the server and
// which therefore stays out of every log.

// VerifyEmail is the address verification mail (valid 48 hours).
func VerifyEmail(instanceName, publicURL, to, token string) Message {
	link := publicURL + "/verify-email#token=" + token
	return Message{
		To:      to,
		Subject: "驗證你的電子郵件 / Verify your email address",
		Body: lines(
			"你好，",
			"",
			"請開啟下方連結，驗證你在「"+instanceName+"」使用的電子郵件地址（48 小時內有效）：",
			link,
			"",
			"如果你沒有註冊或變更過電子郵件，請忽略這封信。",
			"",
			"----",
			"",
			"Hello,",
			"",
			"Open the link below to verify your email address for \""+instanceName+"\" (valid for 48 hours):",
			link,
			"",
			"If you did not sign up or change your email address, you can ignore this message.",
		),
	}
}

// ResetPassword is the password reset mail (valid 1 hour).
func ResetPassword(instanceName, publicURL, to, token string) Message {
	link := publicURL + "/reset-password#token=" + token
	return Message{
		To:      to,
		Subject: "重設密碼 / Reset your password",
		Body: lines(
			"你好，",
			"",
			"有人要求重設「"+instanceName+"」帳號的密碼。請開啟下方連結設定新密碼（1 小時內有效，只能使用一次）：",
			link,
			"",
			"如果不是你本人操作，請忽略這封信，密碼不會改變。",
			"",
			"----",
			"",
			"Hello,",
			"",
			"Someone asked to reset the password of your \""+instanceName+"\" account. Open the link below to choose a new one (valid for 1 hour, single use):",
			link,
			"",
			"If this was not you, ignore this message; your password stays the same.",
		),
	}
}

func lines(l ...string) string { return strings.Join(l, "\n") + "\n" }
