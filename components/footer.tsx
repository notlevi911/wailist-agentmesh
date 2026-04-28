export function Footer() {
  return (
    <footer className="border-t border-[#141414] px-6 py-8">
      <div className="mx-auto flex max-w-2xl items-center justify-between">
        <span className="text-sm font-bold text-foreground">
          Agent<span className="text-primary">Mesh</span>
        </span>
        <div className="flex gap-6">
          <a
            href="https://github.com/notlevi911/agentmesh"
            className="text-xs text-[#374151] transition-colors hover:text-[#6b7280]"
            target="_blank"
            rel="noreferrer"
          >
            GitHub
          </a>
          <a
            href="https://algorand.co/agentic-commerce/x402"
            className="text-xs text-[#374151] transition-colors hover:text-[#6b7280]"
            target="_blank"
            rel="noreferrer"
          >
            Algorand
          </a>
          <a
            href="https://www.x402.org"
            className="text-xs text-[#374151] transition-colors hover:text-[#6b7280]"
            target="_blank"
            rel="noreferrer"
          >
            x402
          </a>
        </div>
      </div>
    </footer>
  );
}
