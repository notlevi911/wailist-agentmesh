import { NextRequest, NextResponse } from "next/server";
import { getSupabase } from "@/lib/supabase";

export async function POST(req: NextRequest) {
  const body = await req.json();
  const { email, use_case, invite_code } = body;

  if (!email || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
    return NextResponse.json({ error: "Valid email required" }, { status: 400 });
  }

  const { error } = await getSupabase().from("waitlist").insert({
    email,
    use_case: use_case || null,
    invite_code: invite_code || null,
  });

  if (error) {
    if (error.code === "23505") {
      return NextResponse.json(
        { error: "Already on the waitlist" },
        { status: 409 }
      );
    }
    return NextResponse.json({ error: "Something went wrong" }, { status: 500 });
  }

  return NextResponse.json({ success: true });
}
