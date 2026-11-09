import { describe, expect, it } from "vitest"
import { addDays, dated, daysBetween, formatDate, hourIn, parseDate, relativeDays, toDateString, todayIn } from "./dates"

describe("parseDate", () => {
  it("reads day, month and year precision", () => {
    expect(parseDate("2026-11-05")).toEqual({ date: "2026-11-05", precision: "day" })
    expect(parseDate("2027-12")).toEqual({ date: "2027-12-01", precision: "month" })
    expect(parseDate("2027")).toEqual({ date: "2027-01-01", precision: "year" })
    expect(parseDate(" 2026-11-05T09:30:00Z ")).toEqual({ date: "2026-11-05", precision: "day" })
    expect(parseDate("2026-11-05T09:30:00+08:00")).toEqual({ date: "2026-11-05", precision: "day" })
  })

  it("rejects impossible and unreadable dates", () => {
    for (const bad of ["2026-02-30", "2026-13", "2026-00-10", "next friday", "11/05/2026", "", "1850", "26-11-05", "2026-1-5"]) {
      expect(parseDate(bad), bad).toBeNull()
    }
    expect(parseDate(null)).toBeNull()
    expect(parseDate("2028-02-29")).toEqual({ date: "2028-02-29", precision: "day" })
  })

  it("round-trips the short form", () => {
    for (const s of ["2026-11-05", "2027-12", "2027"]) expect(toDateString(parseDate(s)!)).toBe(s)
  })

  it("reads stored columns and drops broken ones", () => {
    expect(dated("2027-12-01", "month")).toEqual({ date: "2027-12-01", precision: "month" })
    expect(dated("2027-12-01", null)).toBeNull()
    expect(dated(null, "day")).toBeNull()
    expect(dated("2027-12", "month")).toBeNull()
    expect(dated("2027-12-01", "week")).toBeNull()
  })
})

describe("formatting and arithmetic", () => {
  it("formats by precision without time zone shifts", () => {
    expect(formatDate({ date: "2026-11-05", precision: "day" })).toBe("Nov 5, 2026")
    expect(formatDate({ date: "2027-12-01", precision: "month" })).toBe("Dec 2027")
    expect(formatDate({ date: "2027-01-01", precision: "year" })).toBe("2027")
  })

  it("adds and counts days across months and years", () => {
    expect(addDays("2026-12-31", 1)).toBe("2027-01-01")
    expect(addDays("2026-03-01", -1)).toBe("2026-02-28")
    expect(daysBetween("2026-11-10", "2027-11-10")).toBe(365)
    expect(daysBetween("2026-11-10", "2026-11-09")).toBe(-1)
  })

  it("says relative days plainly", () => {
    expect([0, 1, -1, 12, -3].map(relativeDays)).toEqual(["today", "tomorrow", "yesterday", "in 12 days", "3 days ago"])
  })

  it("knows today and the hour in a time zone", () => {
    const t = Date.UTC(2026, 10, 9, 20, 0) // 20:00 UTC is 04:00 next day in Makassar (UTC+8)
    expect(todayIn("UTC", t)).toBe("2026-11-09")
    expect(todayIn("Asia/Makassar", t)).toBe("2026-11-10")
    expect(hourIn("Asia/Makassar", t)).toBe(4)
    expect(todayIn("Not/AZone", t)).toBe("2026-11-09")
  })
})
