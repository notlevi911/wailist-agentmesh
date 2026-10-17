"use client";
import { useEffect, useId, useMemo, useState } from "react";
import { AlertCircle, CalendarClock, Info } from "lucide-react";
import { workflows as workflowsApi } from "@/lib/api";
import {
  cadenceToCron,
  cronToCadence,
  selectedDaysOfWeek,
  type Cadence,
  type CadenceValue,
} from "@/lib/cronCadence";
import {
  describeCadence,
  describeNextRun,
  nextLocalRun,
  observesDaylightSaving,
  ordinal,
  timeZoneLabel,
} from "@/lib/scheduleText";
import { scheduleErrorText } from "@/lib/scheduleError";
import { cn } from "@/lib/utils";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/ScheduleSkeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";

// The schedule editor in the desktop row menu (DesktopRowMenu), built from
// shadcn/ui. Compact viewports and the native shell keep RowMenu's original
// popover. Everything shown here is plain English in the user's timezone:
// the UTC cron is only what gets sent to the server.

const WEEK = [
  { dow: 1, short: "Mon", long: "Monday" },
  { dow: 2, short: "Tue", long: "Tuesday" },
  { dow: 3, short: "Wed", long: "Wednesday" },
  { dow: 4, short: "Thu", long: "Thursday" },
  { dow: 5, short: "Fri", long: "Friday" },
  { dow: 6, short: "Sat", long: "Saturday" },
  { dow: 0, short: "Sun", long: "Sunday" },
];
const MONTH_DAYS = Array.from({ length: 28 }, (_, i) => i + 1);
const TIME_PRESETS = [
  { value: "06:00", label: "6 AM" },
  { value: "09:00", label: "9 AM" },
  { value: "12:00", label: "Noon" },
  { value: "18:00", label: "6 PM" },
];
// shadcn's toggle marks "on" with the same grey as hover; the brand tint
// makes the chosen day or time readable at a glance.
const SELECTED =
  "data-[state=on]:bg-primary/15 data-[state=on]:text-primary data-[state=on]:font-medium";

const DEFAULTS: CadenceValue = {
  cadence: "daily",
  time: "09:00",
  dayOfWeek: 1,
  dayOfMonth: 1,
};

type Stored = { cron?: string; nextRunAt?: string };
type Load =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; stored: Stored };

export function DesktopSchedulePanel({
  workflowId,
  workflowName,
  onSave,
  onRemove,
  onClose,
}: {
  workflowId: string;
  workflowName?: string;
  onSave: (cron: string) => Promise<void>;
  onRemove: () => Promise<void>;
  onClose: () => void;
}) {
  // Read on every open so a stale list cannot overwrite a newer schedule.
  // Failed reads must not fall back to editable defaults.
  const [load, setLoad] = useState<Load>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let live = true;
    workflowsApi.get(workflowId).then(
      (wf) => {
        if (live) {
          setLoad({
            status: "ready",
            stored: { cron: wf.scheduleCron, nextRunAt: wf.scheduleNextRunAt },
          });
        }
      },
      (e: unknown) => {
        if (live) {
          setLoad({
            status: "error",
            message: scheduleErrorText(e, "load"),
          });
        }
      },
    );
    return () => {
      live = false;
    };
  }, [workflowId, attempt]);

  const active = load.status === "ready" && !!load.stored.cron;

  return (
    <div className="flex flex-col">
      <div className="flex items-start justify-between gap-3 px-4 pt-4 pb-3">
        <div className="min-w-0">
          <h2 className="text-sm leading-none font-semibold">Schedule</h2>
          {workflowName && (
            <p className="mt-1.5 truncate text-xs text-muted-foreground">
              {workflowName}
            </p>
          )}
        </div>
        {active && (
          <Badge variant="secondary" className="gap-1.5">
            <span
              className="size-1.5 rounded-full bg-primary"
              aria-hidden="true"
            />
            Active
          </Badge>
        )}
      </div>
      <Separator />

      {load.status === "loading" && (
        <div className="grid gap-4 p-4" aria-busy="true">
          <Skeleton className="h-9 w-full motion-reduce:animate-none" />
          <Skeleton className="h-9 w-2/3 motion-reduce:animate-none" />
          <Skeleton className="h-16 w-full motion-reduce:animate-none" />
        </div>
      )}

      {load.status === "error" && (
        <>
          <div className="p-4">
            <Alert variant="destructive">
              <AlertCircle />
              <AlertTitle>Couldn&apos;t load the schedule</AlertTitle>
              <AlertDescription>{load.message}</AlertDescription>
            </Alert>
          </div>
          <Separator />
          <div className="flex justify-end gap-2 px-4 py-3">
            <Button type="button" variant="ghost" size="sm" onClick={onClose}>
              Close
            </Button>
            <Button
              type="button"
              size="sm"
              onClick={() => {
                setLoad({ status: "loading" });
                setAttempt((n) => n + 1);
              }}
            >
              Try again
            </Button>
          </div>
        </>
      )}

      {load.status === "ready" && (
        <ScheduleForm
          stored={load.stored}
          onSave={onSave}
          onRemove={onRemove}
          onClose={onClose}
        />
      )}
    </div>
  );
}

function ScheduleForm({
  stored,
  onSave,
  onRemove,
  onClose,
}: {
  stored: Stored;
  onSave: (cron: string) => Promise<void>;
  onRemove: () => Promise<void>;
  onClose: () => void;
}) {
  const ids = useId();
  const storedValue = useMemo(
    () => (stored.cron ? cronToCadence(stored.cron) : null),
    [stored.cron],
  );
  // A schedule this editor didn't write (set straight through the API, say)
  // can't be shown as a cadence; say so rather than pretend it is the default.
  const custom = !!stored.cron && !storedValue;
  const initial = storedValue ?? DEFAULTS;

  const [cadence, setCadence] = useState<Cadence>(initial.cadence);
  const [time, setTime] = useState(initial.time);
  const [daysOfWeek, setDaysOfWeek] = useState(() => selectedDaysOfWeek(initial));
  const [dayOfMonth, setDayOfMonth] = useState(initial.dayOfMonth ?? 1);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [removeArmed, setRemoveArmed] = useState(false);
  const [replacingCustom, setReplacingCustom] = useState(false);
  const editing = !custom || replacingCustom;

  const value: CadenceValue = { cadence, time, daysOfWeek, dayOfMonth };
  const timeValid = /^\d{2}:\d{2}$/.test(time);

  // Built on every render, so a monthly time that can't repeat is flagged
  // while it is being picked rather than after Save.
  let cron: string | null = null;
  let unschedulable = false;
  if (timeValid) {
    try {
      cron = cadenceToCron(value);
    } catch {
      unschedulable = true;
    }
  }
  const unchanged = !!stored.cron && cron === stored.cron;
  const now = new Date();
  // The server's next run is the truth for the saved schedule -- unless it
  // is already in the past (the scheduler hasn't caught up), where the
  // preview's own answer is the better one to show.
  const storedNext = stored.nextRunAt ? new Date(stored.nextRunAt) : null;
  const nextRun =
    unchanged && storedNext && storedNext > now
      ? storedNext
      : cron
        ? nextLocalRun(value, now)
        : null;

  const save = async () => {
    if (saving || !editing || !cron || unchanged) return;
    setSaving(true);
    setError(null);
    try {
      await onSave(cron);
    } catch (e) {
      setError(scheduleErrorText(e));
      setSaving(false);
    }
  };

  const remove = async () => {
    if (!removeArmed) {
      setRemoveArmed(true);
      return;
    }
    setSaving(true);
    setError(null);
    try {
      await onRemove();
    } catch (e) {
      setError(scheduleErrorText(e, "remove"));
      setSaving(false);
      setRemoveArmed(false);
    }
  };

  const timeField = (
    <div className="grid gap-2">
      <Label htmlFor={`${ids}-time`}>Time</Label>
      <div className="flex items-center gap-3">
        <Input
          id={`${ids}-time`}
          type="time"
          required
          value={time}
          onChange={(e) => setTime(e.target.value)}
          className="w-32 tabular-nums [color-scheme:dark]"
        />
        <span className="min-w-0 text-xs leading-snug text-muted-foreground">
          {timeZoneLabel(now)}
        </span>
      </div>
      <ToggleGroup
        type="single"
        variant="outline"
        size="sm"
        spacing={1}
        aria-label="Common times"
        value={TIME_PRESETS.some((p) => p.value === time) ? time : ""}
        onValueChange={(v) => v && setTime(v)}
        className="grid w-full grid-cols-4 gap-1.5"
      >
        {TIME_PRESETS.map((p) => (
          <ToggleGroupItem
            key={p.value}
            value={p.value}
            className={cn("h-7 w-full px-0 text-xs font-normal", SELECTED)}
          >
            {p.label}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
    </div>
  );

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
    >
      <div className="grid gap-4 p-4">
        {custom && (
          <Alert>
            <Info />
            <AlertTitle>This workflow has a custom schedule</AlertTitle>
            <AlertDescription>
              This editor can&apos;t represent the saved schedule. It stays
              unchanged unless you choose to replace or remove it.
              {!editing && (
                <Button type="button" variant="outline" size="sm" onClick={() => setReplacingCustom(true)}>
                  Replace schedule
                </Button>
              )}
            </AlertDescription>
          </Alert>
        )}

        {editing && (
        <Tabs
          value={cadence}
          onValueChange={(v) => setCadence(v as Cadence)}
          className="gap-4"
        >
          <TabsList className="w-full" aria-label="Repeat">
            <TabsTrigger value="daily">Daily</TabsTrigger>
            <TabsTrigger value="weekly">Weekly</TabsTrigger>
            <TabsTrigger value="monthly">Monthly</TabsTrigger>
          </TabsList>

          <TabsContent value="daily" className="grid gap-4">
            {timeField}
          </TabsContent>

          <TabsContent value="weekly" className="grid gap-4">
            <div className="grid gap-2">
              <Label id={`${ids}-dow`}>Days</Label>
              <ToggleGroup
                type="multiple"
                variant="outline"
                size="sm"
                aria-labelledby={`${ids}-dow`}
                value={daysOfWeek.map(String)}
                onValueChange={(days) => days.length > 0 && setDaysOfWeek(days.map(Number))}
                className="w-full"
              >
                {WEEK.map((d) => (
                  <ToggleGroupItem
                    key={d.dow}
                    value={String(d.dow)}
                    aria-label={d.long}
                    aria-disabled={daysOfWeek.length === 1 && daysOfWeek.includes(d.dow)}
                    className={cn("flex-1 px-0 text-xs font-normal", SELECTED)}
                  >
                    {d.short}
                  </ToggleGroupItem>
                ))}
              </ToggleGroup>
              <p className="text-xs text-muted-foreground">Select one or more days.</p>
            </div>
            {timeField}
          </TabsContent>

          <TabsContent value="monthly" className="grid gap-4">
            <div className="grid gap-2">
              <div className="flex items-baseline justify-between">
                <Label id={`${ids}-dom`}>Day of the month</Label>
                <span className="text-xs text-muted-foreground">
                  the {ordinal(dayOfMonth)}
                </span>
              </div>
              <ToggleGroup
                type="single"
                variant="outline"
                size="sm"
                spacing={1}
                aria-labelledby={`${ids}-dom`}
                value={String(dayOfMonth)}
                onValueChange={(v) => v && setDayOfMonth(Number(v))}
                className="grid w-full grid-cols-7 gap-1"
              >
                {MONTH_DAYS.map((d) => (
                  <ToggleGroupItem
                    key={d}
                    value={String(d)}
                    aria-label={`The ${ordinal(d)}`}
                    className={cn(
                      "h-7 w-full px-0 text-xs font-normal tabular-nums",
                      SELECTED,
                    )}
                  >
                    {d}
                  </ToggleGroupItem>
                ))}
              </ToggleGroup>
              <p className="text-xs text-muted-foreground">
                Days 29 to 31 aren&apos;t offered, since not every month has
                them.
              </p>
            </div>
            {timeField}
          </TabsContent>
        </Tabs>
        )}

        {editing && (unschedulable ? (
          <Alert variant="destructive">
            <AlertCircle />
            <AlertTitle>This time can&apos;t repeat monthly</AlertTitle>
            <AlertDescription>
              On some months it would land on a different day. Try a different
              time or day.
            </AlertDescription>
          </Alert>
        ) : nextRun ? (
          <Alert>
            <CalendarClock />
            <AlertTitle className="line-clamp-none text-balance">
              {keepTimesTogether(describeCadence(value))}
            </AlertTitle>
            <AlertDescription className="text-pretty">
              {keepTimesTogether(describeNextRun(nextRun, now))}
            </AlertDescription>
          </Alert>
        ) : (
          <p className="text-xs text-muted-foreground">Pick a time.</p>
        ))}

        {observesDaylightSaving(now) && (
          <p className="text-xs leading-relaxed text-muted-foreground">
            Runs can move by an hour when your clocks change for daylight
            saving.
          </p>
        )}

        {error && (
          <Alert variant="destructive">
            <AlertCircle />
            <AlertTitle>Something went wrong</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
      </div>

      <Separator />
      <div className="flex items-center justify-between gap-2 px-4 py-3">
        {stored.cron ? (
          <Button
            type="button"
            size="sm"
            variant={removeArmed ? "destructive" : "ghost"}
            className={removeArmed ? undefined : "text-destructive"}
            disabled={saving}
            onClick={() => void remove()}
            onBlur={() => setRemoveArmed(false)}
          >
            {removeArmed ? "Confirm remove" : "Remove"}
          </Button>
        ) : (
          <span />
        )}
        <div className="flex gap-2">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            disabled={saving}
            onClick={onClose}
          >
            Cancel
          </Button>
          <Button
            type="submit"
            size="sm"
            disabled={saving || !editing || !cron || unchanged}
            title={unchanged ? "No changes to save" : undefined}
          >
            {saving ? "Saving…" : stored.cron ? "Update" : "Save schedule"}
          </Button>
        </div>
      </div>
    </form>
  );
}

// "9:00 AM" and "(in 4 days)" never break across two lines.
function keepTimesTogether(s: string): string {
  return s
    .replace(/ (AM|PM)\b/g, "\u00a0$1")
    .replace(/\([^)]*\)/g, (m) => m.replace(/ /g, "\u00a0"));
}
