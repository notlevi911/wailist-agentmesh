"use client";

import { motion } from "framer-motion";
import { TextRevealByWord } from "@/components/ui/text-reveal";

const blocks = [
  {
    heading: "Design agent workflows visually.",
    body: "Drag nodes onto a canvas. Connect agents to tools and to each other. The graph is the execution graph — the backend enforces it at runtime.",
    borderColor: "#22c55e",
  },
  {
    heading: "Fund agents with real Algorand wallets.",
    body: "Every deployed agent node gets an Ed25519 keypair on Algorand testnet. Fund it from the UI, watch balances update live via Algod RPC.",
    borderColor: "#374151",
  },
  {
    heading: "Run pipelines with on-chain payments.",
    body: "Agent wallets pay for tool calls via x402 micropayments. Every A2A message is anchored on Algorand for a verifiable audit trail.",
    borderColor: "rgba(109, 40, 217, 0.3)",
  },
];

export function InfoSection() {
  return (
    <section>
      <TextRevealByWord text="Visual workflow design meets on-chain agentic commerce. No custom orchestration code. No manual wallet management." />

      <div className="mx-auto max-w-2xl px-6 pb-28">
        <p className="mb-12 text-xs font-semibold tracking-[2.5px] text-[#4b5563] uppercase">
          What is AgentMesh
        </p>

        <div className="flex flex-col gap-12">
          {blocks.map((block, i) => (
            <motion.div
              key={i}
              initial={{ opacity: 0, y: 24 }}
              whileInView={{ opacity: 1, y: 0 }}
              viewport={{ once: true, margin: "-80px" }}
              transition={{ duration: 0.5, delay: i * 0.1 }}
              style={{ borderLeftColor: block.borderColor }}
              className="border-l-2 pl-6"
            >
              <h3 className="mb-2 text-xl font-semibold leading-snug text-foreground">
                {block.heading}
              </h3>
              <p className="text-sm leading-relaxed text-[#6b7280]">{block.body}</p>
            </motion.div>
          ))}
        </div>
      </div>
    </section>
  );
}
