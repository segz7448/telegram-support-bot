package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// isoCodes: currency codes the fallback source may be asked about. Only used when BOTH words are codes and neither
// is a unit, so "5 km in miles" is never mistaken for money.
var isoCodes = set(strings.Fields(`aed afn all amd ang aoa ars aud awg azn bam bbd bdt bgn bhd bif bmd bnd bob brl bsd btn bwp byn bzd cad cdf chf clp cny cop crc cup cve czk djf dkk dop dzd egp ern etb eur fjd fkp fok gbp gel ggp ghs gip gmd gnf gtq gyd hkd hnl hrk htg huf idr ils imp inr iqd irr isk jep jmd jod jpy kes kgs khr kid kmf krw kwd kyd kzt lak lbp lkr lrd lsl lyd mad mdl mga mkd mmk mnt mop mru mur mvr mwk mxn myr mzn nad ngn nio nok npr nzd omr pab pen pgk php pkr pln pyg qar ron rsd rub rwf sar sbd scr sdg sek sgd shp sle sos srd ssp stn syp szl thb tjs tmt tnd top try ttd tvd twd tzs uah ugx usd uyu uzs ves vnd vuv wst xaf xcd xdr xof xpf yer zar zmw zwl`))

const erTTL = 6 * time.Hour // the source updates once a day and asks callers not to poll it

// currencyER converts via ExchangeRate-API's open endpoint (no key; attribution required; caching allowed). It is
// used only for currencies outside the ECB list. One fetch per six hours covers every conversion.
func (t *Tools) currencyER(v float64, from, to string) string {
	if t.ErAPI == "" { // off unless the owner enables it (TG_FX_FALLBACK=1): provider terms are still being checked
		return "I only know the currencies in the European Central Bank's daily list (like USD, EUR, GBP, JPY), so I can't convert " + from + " or " + to + " yet."
	}
	if t.erRates == nil || t.Now().Sub(t.erAt) > erTTL {
		if !t.allow() {
			return "I'm doing a lot of lookups right now. Please try again in a minute."
		}
		b, st, err := t.get(t.ErAPI, 15)
		if err != nil || st == 0 || st >= 500 || st == 429 {
			t.logf("lookup failed (fx2)")
			return fmt.Sprintf(unreachable, "the exchange-rate service")
		}
		var d struct {
			Result string             `json:"result"`
			Update string             `json:"time_last_update_utc"`
			Rates  map[string]float64 `json:"rates"`
		}
		if json.Unmarshal(b, &d) != nil || d.Result != "success" || len(d.Rates) < 30 {
			return fmt.Sprintf(unreachable, "the exchange-rate service")
		}
		t.erRates, t.erDate, t.erAt = d.Rates, d.Update, t.Now()
		t.logf("lookup ok (fx2)")
	}
	rf, rt := t.erRates[from], t.erRates[to]
	if rf <= 0 || rt <= 0 {
		return "I don't have a rate for " + from + " or " + to + "."
	}
	r := v / rf * rt
	if math.Abs(r) >= 1 {
		r = math.Round(r*100) / 100
	} else {
		r = math.Round(r*1e6) / 1e6
	}
	date := t.erDate
	if i := strings.Index(date, " 00:"); i > 0 {
		date = date[:i]
	}
	return fmt.Sprintf("%s %s = %s %s\nDaily rate from ExchangeRate-API (https://www.exchangerate-api.com), updated %s. Indicative only, not what a bank or shop will give you.", fmtNum(v), from, fmtNum(r), to, date)
}
