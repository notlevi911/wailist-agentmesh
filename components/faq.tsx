import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";

const faqs = [
  {
    q: "What is AgentMesh?",
    a: "AgentMesh is a visual drag-and-drop canvas for building, funding, and running autonomous AI agent pipelines on Algorand. You design workflows visually, deploy them as live HTTP endpoints, fund agent wallets on-chain, and run them — all from one interface.",
  },
  {
    q: "Do I need ALGO to use it?",
    a: "For testnet, no — the Algorand testnet faucet covers agent wallet funding for free. On mainnet, agents need small amounts of ALGO for transaction fees and x402 tool payments.",
  },
  {
    q: "What is x402?",
    a: "x402 is an HTTP-native micropayment standard built on the HTTP 402 status code. It lets agents pay for API access per request — no accounts, no subscriptions. Algorand's x402 support is live via GoPlausible.",
  },
  {
    q: "Is it open source?",
    a: "Yes. The AgentMesh canvas, runtime, and Algorand integration are fully open source. Check the GitHub repo for the full codebase.",
  },
];

export function FAQ() {
  return (
    <section className="mx-auto max-w-2xl px-6 pb-28">
      <p className="mb-8 text-xs font-semibold tracking-[2.5px] text-[#4b5563] uppercase">
        FAQ
      </p>
      <Accordion multiple={false}>
        {faqs.map((faq, i) => (
          <AccordionItem
            key={i}
            value={i}
            className="border-b border-[#141414]"
          >
            <AccordionTrigger className="py-4 text-sm font-medium text-[#d1d5db] hover:text-foreground hover:no-underline [&>svg]:text-primary">
              {faq.q}
            </AccordionTrigger>
            <AccordionContent className="pb-4 text-sm leading-relaxed text-[#6b7280]">
              {faq.a}
            </AccordionContent>
          </AccordionItem>
        ))}
      </Accordion>
    </section>
  );
}
