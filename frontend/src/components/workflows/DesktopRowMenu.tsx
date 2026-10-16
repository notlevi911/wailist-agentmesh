"use client";
import { useRef, useState } from "react";
import { CalendarClock, Ellipsis, Share2, Trash2 } from "lucide-react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Popover,
  PopoverAnchor,
  PopoverContent,
} from "@/components/ui/popover";
import { DesktopSchedulePanel } from "./DesktopSchedulePanel";

// The ⋯ menu on a workflow row, on desktop. Compact viewports and the native
// shell use RowMenu (WorkflowsPage.tsx), subject to the device policy.
//
// Three shadcn/ui layers share the one ⋯ button: the menu itself, the
// schedule editor (a popover anchored to the button, so it stays beside the
// row it belongs to), and the delete confirmation (an alert dialog, since
// deleting cannot be undone).
export function DesktopRowMenu({
  workflowId,
  workflowName,
  deployed,
  scheduleCron,
  onDelete,
  onShare,
  onSetSchedule,
  onClearSchedule,
}: {
  workflowId: string;
  workflowName?: string;
  deployed: boolean;
  scheduleCron?: string;
  onDelete: () => void;
  onShare: () => void;
  onSetSchedule: (cron: string) => Promise<void>;
  onClearSchedule: () => Promise<void>;
}) {
  const [scheduleOpen, setScheduleOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  // Which layer a menu item asked for. It opens only once the menu has
  // finished closing: opened any sooner, the popover lands under the pointer,
  // the menu item beneath it loses hover, Radix moves focus back into the
  // still-closing menu, and the popover takes that as a click away and shuts.
  const pending = useRef<"schedule" | "delete" | null>(null);
  // Radix returns focus to the button only when a layer closed without the
  // user clicking elsewhere; same rule here for the popover, whose anchor
  // (unlike a PopoverTrigger) Radix doesn't track.
  const clickedAway = useRef(false);

  const restoreFocus = (e: Event) => {
    e.preventDefault();
    if (!clickedAway.current) triggerRef.current?.focus();
    clickedAway.current = false;
  };

  return (
    // The row itself navigates on click. Events from the portalled menu,
    // popover and dialog still bubble through React to here, so stop them.
    <div onClick={(e) => e.stopPropagation()}>
      <Popover open={scheduleOpen} onOpenChange={setScheduleOpen}>
        <DropdownMenu modal={false}>
          <PopoverAnchor asChild>
            <DropdownMenuTrigger asChild>
              <Button
                ref={triggerRef}
                variant="outline"
                size="icon-sm"
                aria-label="Workflow actions"
                // Matches the row's Open / Zone buttons at rest.
                className="size-7 rounded-md bg-transparent text-muted-foreground shadow-none hover:text-foreground data-[state=open]:bg-accent data-[state=open]:text-foreground dark:bg-transparent"
              >
                <Ellipsis />
              </Button>
            </DropdownMenuTrigger>
          </PopoverAnchor>
          <DropdownMenuContent
            align="end"
            className="w-52"
            onCloseAutoFocus={(e) => {
              const next = pending.current;
              if (!next) return;
              pending.current = null;
              // Focus goes into the popover or dialog, not back to ⋯.
              e.preventDefault();
              if (next === "schedule") setScheduleOpen(true);
              else setDeleteOpen(true);
            }}
          >
            <DropdownMenuItem
              disabled={!deployed}
              onSelect={() => {
                pending.current = "schedule";
              }}
            >
              <CalendarClock />
              {scheduleCron ? "Edit schedule" : "Schedule"}
              {!deployed && (
                <DropdownMenuShortcut className="tracking-normal">
                  Deploy first
                </DropdownMenuShortcut>
              )}
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={onShare}>
              <Share2 />
              Share
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              variant="destructive"
              onSelect={() => {
                pending.current = "delete";
              }}
            >
              <Trash2 />
              Delete workflow
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        <PopoverContent
          // Beside the button rather than below it: the editor is taller than
          // the room under (or over) most rows, while the left side has the
          // whole window height to centre in. Scrolls as a last resort on a
          // very short window.
          side="left"
          align="center"
          sideOffset={8}
          collisionPadding={12}
          aria-label="Schedule"
          className="max-h-(--radix-popover-content-available-height) w-88 overflow-y-auto p-0"
          onInteractOutside={() => {
            clickedAway.current = true;
          }}
          onCloseAutoFocus={restoreFocus}
        >
          <DesktopSchedulePanel
            workflowId={workflowId}
            workflowName={workflowName}
            onClose={() => setScheduleOpen(false)}
            onSave={async (cron) => {
              await onSetSchedule(cron);
              setScheduleOpen(false);
            }}
            onRemove={async () => {
              await onClearSchedule();
              setScheduleOpen(false);
            }}
          />
        </PopoverContent>
      </Popover>

      <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <AlertDialogContent size="sm" onCloseAutoFocus={restoreFocus}>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this workflow?</AlertDialogTitle>
            <AlertDialogDescription>
              {workflowName ? (
                <>
                  <span className="font-medium text-foreground">
                    {workflowName}
                  </span>{" "}
                  will be deleted for good. This can&apos;t be undone.
                </>
              ) : (
                "It will be deleted for good. This can't be undone."
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={onDelete}>
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
