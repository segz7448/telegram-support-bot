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

const erMaxAge = 4 * 24 * time.Hour // the source refreshes daily; anything older than this is refused

// erFresh: the source's own update time must be valid, not in the future (1 hour of clock skew allowed) and no
// older than erMaxAge.
func erFresh(ts, now time.Time) bool {
	if ts.IsZero() || ts.After(now.Add(time.Hour)) {
		return false
	}
	return now.Sub(ts) <= erMaxAge
}

const erTTL = 6 * time.Hour // the source updates once a day and asks callers not to poll it

// currencyER converts via ExchangeRate-API's open endpoint (no key). Its open-access page and terms (read 2026-11-17:
// exchangerate-api.com/docs/free and /terms) allow caching and personal or commercial conversion use, forbid
// re-distribution, and require an attribution link where the rates are shown: every reply here carries the link. It is
// used only for currencies outside the ECB list. One fetch per six hours covers every conversion.
func (t *Tools) currencyER(v float64, from, to string) string {
	if t.ErAPI == "" { // off only if TG_FX_FALLBACK=0 (see the terms note above)
		return "I only know the currencies in the European Central Bank's daily list (like USD, EUR, GBP, JPY), so I can't convert " + from + " or " + to + " yet."
	}
	if t.erRates != nil && !erFresh(t.erTS, t.Now()) { // a cached table that has aged out is dropped, never served
		t.erRates = nil
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
		// Refuse stale data: the source says when it last updated, and old rates would mislead.
		ts, perr := time.Parse(time.RFC1123Z, d.Update)
		if perr != nil || !erFresh(ts, t.Now()) {
			t.logf("fx2 data stale or undated")
			return "The exchange-rate source's data looks out of date (last update " + d.Update + "), so I won't give you a rate from it."
		}
		t.erRates, t.erDate, t.erAt, t.erTS = d.Rates, d.Update, t.Now(), ts
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
	return fmt.Sprintf("%s %s = %s %s\nRates By Exchange Rate API: https://www.exchangerate-api.com (daily rate, updated %s). Indicative only, not what a bank or shop will give you.", fmtNum(v), from, fmtNum(r), to, date)
}
