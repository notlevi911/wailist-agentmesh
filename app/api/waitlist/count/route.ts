import { NextResponse } from "next/server";
import { getSupabase } from "@/lib/supabase";

export async function GET() {
  const { count, error } = await getSupabase()
    .from("waitlist")
    .select("*", { count: "exact", head: true });

  if (error) {
    console.error("waitlist count error:", error);
    return NextResponse.json({ error: "Failed to fetch count" }, { status: 500 });
  }

  return NextResponse.json({ count: count ?? 0 });
}
