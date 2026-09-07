import {
  siAnthropic,
  siGooglegemini,
  siMistralai,
  siDiscord,
  siGooglechat,
  siNtfy,
  siTelegram,
  siGithub,
  siGitlab,
  siJira,
  siLinear,
  siSentry,
  siNotion,
  siAirtable,
  siTrello,
  siAsana,
  siClickup,
  siTodoist,
  siHubspot,
  siMailchimp,
  siSupabase,
  siWoocommerce,
  siElevenlabs,
  type SimpleIcon,
} from "simple-icons";
import type { ReactNode } from "react";
import { SimpleIconMark, SlackMark, TeamsMark } from "@/components/brand/marks";

// Maps a node template id to its simple-icons export. The logo is drawn in
// currentColor (monochrome) so it inherits the exact colour the placeholder
// letter used -- the node's icon box, size, and styling are untouched; only the
// glyph shape changes from a letter to the service's real mark.
//
// Imports are static (not a dynamic simpleIcons[name] lookup) so the bundler
// can tree-shake everything but these ~23 icons instead of shipping all
// ~3450 in simple-icons. OpenAI and Groq are deliberately absent: simple-icons
// no longer ships either mark (removed at the brands' request, like Slack and
// Microsoft Teams below), and unlike Slack/Teams no custom mark has been drawn
// for them yet, so those two templates fall back to the placeholder letter.
const BRAND_ICONS: Record<string, SimpleIcon> = {
  // LLM providers
  anthropic: siAnthropic,
  gemini: siGooglegemini,
  mistral: siMistralai,
  // Messaging
  discord: siDiscord,
  google_chat: siGooglechat,
  ntfy: siNtfy,
  telegram: siTelegram,
  telegram_get_updates: siTelegram,
  // Dev tools
  github: siGithub,
  gitlab: siGitlab,
  jira: siJira,
  linear: siLinear,
  sentry: siSentry,
  // Productivity
  notion: siNotion,
  airtable: siAirtable,
  trello: siTrello,
  asana: siAsana,
  clickup: siClickup,
  todoist: siTodoist,
  // Data / commerce
  hubspot: siHubspot,
  mailchimp: siMailchimp,
  supabase: siSupabase,
  woocommerce: siWoocommerce,
  // Media
  elevenlabs: siElevenlabs,
};

function iconFor(template?: string): SimpleIcon | undefined {
  if (!template) return undefined;
  return BRAND_ICONS[template];
}

// Brands simple-icons no longer ships -- Slack and Microsoft Teams were both
// removed at the brands' request. Their marks are drawn by hand in
// components/brand/marks, on the same 24-unit grid as everything else.
const CUSTOM_LOGOS: Record<string, (size: number) => ReactNode> = {
  slack: (size) => <SlackMark size={size} />,
  teams: (size) => <TeamsMark size={size} />,
};

// Renders the service's real brand mark in place of the placeholder letter when
// one exists for the node's template; otherwise renders the letter unchanged.
export function BrandLogo({
  template,
  fallback,
  size = 15,
}: {
  template?: string;
  fallback: ReactNode;
  size?: number;
}) {
  const custom = template ? CUSTOM_LOGOS[template] : undefined;
  if (custom) return <>{custom(size)}</>;

  const icon = iconFor(template);
  if (!icon) return <>{fallback}</>;
  return <SimpleIconMark icon={icon} size={size} />;
}
