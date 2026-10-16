import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

// shadcn/ui's class helper: joins conditional class names and lets a later
// Tailwind utility override an earlier one (`cn("px-2", "px-4")` -> "px-4").
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
