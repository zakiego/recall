import * as React from "react"
import { createFileRoute, Link, useRouter } from "@tanstack/react-router"
import { MailWarningIcon, SendIcon } from "lucide-react"

import { AppError } from "@/components/app-error"
import { PageHeader } from "@/components/page-header"
import { SimpleSelect } from "@/components/simple-select"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { Field, FieldContent, FieldDescription, FieldError, FieldGroup, FieldLabel, FieldLegend, FieldSet, FieldTitle } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { toast } from "@/components/ui/toast"
import { getReminders, saveReminders, sendReminderTest } from "@/lib/fns"
import { errorMessage } from "@/lib/format"

export const Route = createFileRoute("/_app/settings/reminders")({
  loader: () => getReminders(),
  pendingComponent: () => (
    <div className="flex flex-col gap-6" aria-busy="true" aria-label="Loading reminder settings">
      <Skeleton className="h-7 w-40" />
      <Skeleton className="h-96 w-full max-w-3xl rounded-xl" />
    </div>
  ),
  errorComponent: AppError,
  head: () => ({ meta: [{ title: "Reminders · Recall" }] }),
  component: RemindersPage,
})

const PRESETS = [90, 30, 14, 7, 3, 1, 0]

function leadLabel(d: number) {
  return d === 0 ? "On the day" : `${d} ${d === 1 ? "day" : "days"} before`
}

function browserZone() {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC"
  } catch {
    return "UTC"
  }
}

function zones(current: string) {
  let list: string[] = []
  try {
    list = Intl.supportedValuesOf("timeZone")
  } catch {
    list = []
  }
  return [...new Set(["UTC", current, ...list])].map((z) => ({ value: z, label: z.replace(/_/g, " ") }))
}

function RemindersPage() {
  const { settings, configured, from } = Route.useLoaderData()
  const router = useRouter()
  const [enabled, setEnabled] = React.useState(settings.enabled)
  const [email, setEmail] = React.useState(settings.email ?? "")
  const [leads, setLeads] = React.useState<number[]>(settings.leadDays)
  const [timezone, setTimezone] = React.useState(settings.timezone)
  const [errors, setErrors] = React.useState<{ email?: string; leads?: string; form?: string }>({})
  const [saving, setSaving] = React.useState(false)
  const [testing, setTesting] = React.useState(false)
  const [savedEmail, setSavedEmail] = React.useState(settings.email)

  // Never saved: start from the browser's zone instead of UTC.
  React.useEffect(() => {
    if (settings.updatedAt === null) setTimezone(browserZone())
  }, [settings.updatedAt])

  const options = React.useMemo(() => zones(timezone), [timezone])
  const choices = [...new Set([...PRESETS, ...settings.leadDays])].sort((a, b) => b - a)

  const toggleLead = (d: number, on: boolean) => setLeads((cur) => (on ? [...new Set([...cur, d])] : cur.filter((x) => x !== d)).sort((a, b) => b - a))

  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    const next: typeof errors = {}
    const trimmed = email.trim()
    if (trimmed && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(trimmed)) next.email = "Enter an email address like you@example.com."
    else if (enabled && !trimmed) next.email = "Add an email address to turn reminders on."
    if (enabled && leads.length === 0) next.leads = "Pick at least one time to be reminded."
    setErrors(next)
    if (next.email || next.leads) {
      document.getElementById(next.email ? "reminders-email" : `lead-${choices[0]}`)?.focus()
      return
    }
    setSaving(true)
    try {
      const saved = await saveReminders({ data: { enabled, email: trimmed, leadDays: leads, timezone } })
      setSavedEmail(saved.email)
      toast.add({ title: saved.enabled ? "Reminders are on" : "Reminders saved", description: saved.enabled ? `Emails go to ${saved.email}.` : undefined, type: "success" })
      void router.invalidate()
    } catch (err) {
      setErrors({ form: errorMessage(err) })
    } finally {
      setSaving(false)
    }
  }

  const test = async () => {
    setTesting(true)
    try {
      const res = await sendReminderTest()
      toast.add({ title: "Test email sent", description: `Sent to ${res.to}. It can take a minute to arrive.`, type: "success" })
    } catch (err) {
      toast.add({ title: "Couldn't send the test email", description: errorMessage(err), type: "error" })
    } finally {
      setTesting(false)
    }
  }

  return (
    <div className="page-in flex flex-col gap-6">
      <PageHeader
        title="Reminders"
        description={
          <>
            An email before a fact is due: a contract ending, a renewal, a deadline. It covers every workspace you're in, and lists what's coming
            on the{" "}
            <Link to="/upcoming" className="font-medium text-foreground underline underline-offset-4">
              Upcoming
            </Link>{" "}
            page.
          </>
        }
      />

      <div className="grid max-w-3xl gap-6">
        {!configured && (
          <Alert>
            <MailWarningIcon />
            <AlertTitle>Email isn't configured on this server</AlertTitle>
            <AlertDescription>
              <p>
                Your settings still save, but nothing is sent until the server sets <code>RESEND_API_KEY</code> and <code>RECALL_EMAIL_FROM</code> (a
                sender on a domain verified in Resend).
              </p>
            </AlertDescription>
          </Alert>
        )}

        <Card>
          <form onSubmit={save} noValidate className="contents">
            <CardHeader>
              <CardTitle>Email reminders</CardTitle>
              <CardDescription>
                One email a day at most, after 08:00 in your time zone, with everything that reached a reminder that day. Each reminder is sent once.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <FieldGroup>
                <Field orientation="horizontal">
                  <FieldContent>
                    <FieldLabel htmlFor="reminders-enabled">Send reminder emails</FieldLabel>
                    <FieldDescription>Turn off to stop every reminder. Your choices below are kept.</FieldDescription>
                  </FieldContent>
                  <Switch id="reminders-enabled" checked={enabled} onCheckedChange={setEnabled} />
                </Field>

                <Field data-invalid={!!errors.email}>
                  <FieldLabel htmlFor="reminders-email">Email address</FieldLabel>
                  <Input
                    id="reminders-email"
                    type="email"
                    autoComplete="email"
                    inputMode="email"
                    spellCheck={false}
                    placeholder="you@example.com"
                    value={email}
                    maxLength={254}
                    aria-invalid={!!errors.email}
                    aria-describedby={errors.email ? "reminders-email-error" : undefined}
                    onChange={(e) => setEmail(e.target.value)}
                  />
                  {errors.email ? (
                    <FieldError id="reminders-email-error">{errors.email}</FieldError>
                  ) : (
                    <FieldDescription>{from ? <>Sent from <span className="wrap-anywhere">{from}</span>.</> : "Where reminders go."}</FieldDescription>
                  )}
                </Field>

                <FieldSet data-invalid={!!errors.leads}>
                  <FieldLegend variant="label">When to remind you</FieldLegend>
                  <FieldDescription>Before the due date. A fact added late gets the next reminder it can still make.</FieldDescription>
                  <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                    {choices.map((d) => (
                      <FieldLabel key={d} htmlFor={`lead-${d}`}>
                        <Field orientation="horizontal">
                          <Checkbox id={`lead-${d}`} checked={leads.includes(d)} onCheckedChange={(on) => toggleLead(d, on === true)} />
                          <FieldContent>
                            <FieldTitle className="tabular-nums">{leadLabel(d)}</FieldTitle>
                          </FieldContent>
                        </Field>
                      </FieldLabel>
                    ))}
                  </div>
                  {errors.leads && <FieldError>{errors.leads}</FieldError>}
                </FieldSet>

                <Field>
                  <FieldLabel htmlFor="reminders-timezone">Time zone</FieldLabel>
                  <SimpleSelect id="reminders-timezone" value={timezone} options={options} onChange={setTimezone} className="w-full sm:w-80" />
                  <FieldDescription>Decides what “today” is and when the 08:00 email goes out.</FieldDescription>
                </Field>

                {errors.form && (
                  <Alert variant="destructive">
                    <AlertTitle>Couldn't save</AlertTitle>
                    <AlertDescription>{errors.form}</AlertDescription>
                  </Alert>
                )}
              </FieldGroup>
            </CardContent>
            <CardFooter className="flex-wrap gap-2">
              <Button type="submit" disabled={saving}>
                {saving && <Spinner data-icon="inline-start" />}
                Save reminders
              </Button>
              <Button
                type="button"
                variant="outline"
                disabled={!configured || !savedEmail || testing}
                onClick={() => void test()}
              >
                {testing ? <Spinner data-icon="inline-start" /> : <SendIcon data-icon="inline-start" />}
                Send a test email
              </Button>
              {configured && !savedEmail && <span className="text-caption text-muted-foreground">Save an address to send a test.</span>}
            </CardFooter>
          </form>
        </Card>
      </div>
    </div>
  )
}
