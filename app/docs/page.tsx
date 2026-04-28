"use client";

import { InfiniteGrid } from "@/components/ui/infinite-grid";
import { AnimatedShinyScramble } from "@/components/ui/animated-shiny-scramble";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { useRouter } from "next/navigation";
import { ShinyButton } from "@/components/ui/shiny-button";

export default function DocsPage() {
  const router = useRouter();
  
  return (
    <div className="relative min-h-screen bg-[#0a0a0a] text-foreground selection:bg-primary/30">
      <nav className="fixed top-0 z-50 w-full border-b border-[#1c1c1c] bg-[#0a0a0a]/80 py-4 backdrop-blur-md">
        <div className="mx-auto flex max-w-5xl items-center justify-between px-6">
          <div className="font-bold tracking-tight text-white cursor-pointer" onClick={() => router.push("/")}>
            AgentMesh
          </div>
          <div className="flex gap-4">
            <button onClick={() => router.push("/")} className="text-sm font-medium text-[#6b7280] hover:text-white transition-colors">Home</button>
            <button onClick={() => router.push("/tutorial")} className="text-sm font-medium text-[#6b7280] hover:text-white transition-colors">Tutorial</button>
          </div>
        </div>
      </nav>

      <InfiniteGrid className="absolute inset-0 z-0 h-[50vh] opacity-50" />
      
      {/* Glow blobs */}
      <div className="pointer-events-none absolute inset-0 overflow-hidden z-0">
        <div className="absolute left-1/2 top-[-80px] h-[500px] w-[500px] -translate-x-1/2 rounded-full bg-[#22c55e]/[0.03] blur-[140px]" />
      </div>

      <main className="relative z-10 mx-auto max-w-4xl px-6 pt-36 pb-24">
        <div className="text-center mb-16">
          <div className="mb-6 inline-flex items-center gap-2 rounded-full border border-[#1c1c1c] bg-[#0f0f0f] px-4 py-1.5 text-xs text-[#4b5563]">
            <span className="h-1.5 w-1.5 rounded-full bg-[#6d28d9]" />
            v1.0 Technical Whitepaper
          </div>
          <h1 className="mb-4 text-4xl font-bold tracking-tight text-white md:text-6xl" style={{ letterSpacing: "-1.5px" }}>
            <AnimatedShinyScramble
              text="Documentation"
              autoPlay
              delay={0}
              gradientColors="linear-gradient(90deg, #ffffff 0%, #f5f0ff 25%, #ffffff 50%, #ede9fe 75%, #ffffff 100%)"
              gradientDuration={3}
            />
          </h1>
          <p className="text-lg text-[#6b7280] max-w-xl mx-auto">
            Build. Fund. Wire. Run. The comprehensive guide to the visual canvas for autonomous AI agent networks on Algorand.
          </p>
        </div>

        <div className="space-y-12">
          
          <section>
            <h2 className="text-2xl font-bold text-white mb-6 tracking-tight">1. Execution Models</h2>
            <div className="grid gap-6 md:grid-cols-2">
              <Card className="border-[#1c1c1c] bg-[#0f0f0f]/50 backdrop-blur-sm">
                <CardHeader>
                  <CardTitle className="text-white">Single-Agent Execution</CardTitle>
                  <CardDescription className="text-[#6b7280]">Used for simpler pipelines with no agent-to-agent wires.</CardDescription>
                </CardHeader>
                <CardContent className="text-sm text-[#8c94a3] leading-relaxed">
                  <ul className="list-disc pl-4 space-y-2 marker:text-[#4b5563]">
                    <li>Graph inspected — single path chosen</li>
                    <li>Agent inspects directly connected tool nodes</li>
                    <li>Gemini planning or heuristic router selects tool path</li>
                    <li>Tool calls executed (with x402 payment if priced)</li>
                    <li>End node returns final output</li>
                  </ul>
                </CardContent>
              </Card>

              <Card className="border-[#1c1c1c] bg-[#0f0f0f]/50 backdrop-blur-sm">
                <CardHeader>
                  <CardTitle className="text-white">Multi-Agent Execution</CardTitle>
                  <CardDescription className="text-[#6b7280]">Independent FastAPI microservices routed via designated wire topologies.</CardDescription>
                </CardHeader>
                <CardContent className="text-sm text-[#8c94a3] leading-relaxed">
                  <ul className="list-disc pl-4 space-y-2 marker:text-[#4b5563]">
                    <li>Graph compiled to runtime config</li>
                    <li>One FastAPI process booted per agent</li>
                    <li>Entry agent triggered via <code className="text-[#a78bfa] bg-[#1c1c1c] px-1 py-0.5 rounded">/run</code></li>
                    <li>Parallel HTTP fan-out using <code className="text-[#a78bfa] bg-[#1c1c1c] px-1 py-0.5 rounded">asyncio.gather</code></li>
                    <li>Downstream synthesis & central log aggregation</li>
                  </ul>
                </CardContent>
              </Card>
            </div>
          </section>

          <section>
            <h2 className="text-2xl font-bold text-white mb-6 tracking-tight">2. Algorand Integration</h2>
            <Card className="border-[#1c1c1c] bg-[#0f0f0f]">
              <CardContent className="grid gap-6 pt-6 sm:grid-cols-2 lg:grid-cols-3">
                
                <div>
                  <h3 className="text-sm font-semibold text-white mb-2 flex items-center gap-2">
                    <span className="h-1.5 w-1.5 rounded-full bg-[#22c55e]"></span> Wallet Creation
                  </h3>
                  <p className="text-xs text-[#8c94a3] leading-relaxed">
                    A real Ed25519 keypair is generated for each agent upon deployment. Persisted safely across backend restarts.
                  </p>
                </div>

                <div>
                  <h3 className="text-sm font-semibold text-white mb-2 flex items-center gap-2">
                    <span className="h-1.5 w-1.5 rounded-full bg-[#22c55e]"></span> Tool Payments
                  </h3>
                  <p className="text-xs text-[#8c94a3] leading-relaxed">
                    Agent wallets instantly sign ALGO payment txns before tool execution via native or AVM x402 paths.
                  </p>
                </div>

                <div>
                  <h3 className="text-sm font-semibold text-white mb-2 flex items-center gap-2">
                    <span className="h-1.5 w-1.5 rounded-full bg-[#22c55e]"></span> A2A Anchoring
                  </h3>
                  <p className="text-xs text-[#8c94a3] leading-relaxed">
                    0-ALGO self-send transactions with message hashes in the tx note act as immutable audit trails.
                  </p>
                </div>

                <div>
                  <h3 className="text-sm font-semibold text-white mb-2 flex items-center gap-2">
                    <span className="h-1.5 w-1.5 rounded-full bg-[#22c55e]"></span> x402 Payment Gate
                  </h3>
                  <p className="text-xs text-[#8c94a3] leading-relaxed">
                    HTTP 402 protocols block execution until confirmed by GoPlausible facilitators.
                  </p>
                </div>

                <div>
                  <h3 className="text-sm font-semibold text-white mb-2 flex items-center gap-2">
                    <span className="h-1.5 w-1.5 rounded-full bg-[#22c55e]"></span> Balance Polling
                  </h3>
                  <p className="text-xs text-[#8c94a3] leading-relaxed">
                    Live balance polling across agent networks fetched via <code className="bg-[#1c1c1c] text-[#a78bfa] rounded px-1">algod</code> RPC APIs.
                  </p>
                </div>

              </CardContent>
            </Card>
          </section>

          <section>
            <h2 className="text-2xl font-bold text-white mb-6 tracking-tight">3. Tool System & Selection</h2>
            <div className="rounded-xl border border-[#1c1c1c] bg-[#0f0f0f] overflow-hidden">
              <table className="w-full text-sm text-left">
                <thead className="bg-[#1a1a1a] text-[#8c94a3] border-b border-[#1c1c1c]">
                  <tr>
                    <th className="px-6 py-3 font-medium">Tool Kind</th>
                    <th className="px-6 py-3 font-medium">Data Source</th>
                    <th className="px-6 py-3 font-medium">Model</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[#1c1c1c] text-[#a1a8b5]">
                  <tr className="hover:bg-[#151515]">
                    <td className="px-6 py-4"><code className="text-[#a78bfa]">weather</code></td>
                    <td className="px-6 py-4">Open-Meteo</td>
                    <td className="px-6 py-4">Free / x402</td>
                  </tr>
                  <tr className="hover:bg-[#151515]">
                    <td className="px-6 py-4"><code className="text-[#a78bfa]">search</code></td>
                    <td className="px-6 py-4">DuckDuckGo</td>
                    <td className="px-6 py-4">Free / x402</td>
                  </tr>
                  <tr className="hover:bg-[#151515]">
                    <td className="px-6 py-4"><code className="text-[#a78bfa]">crypto</code></td>
                    <td className="px-6 py-4">CoinGecko</td>
                    <td className="px-6 py-4">Free / x402</td>
                  </tr>
                  <tr className="hover:bg-[#151515]">
                    <td className="px-6 py-4"><code className="text-[#a78bfa]">chart</code></td>
                    <td className="px-6 py-4">Market Signal</td>
                    <td className="px-6 py-4 text-[#22c55e]">Priced x402</td>
                  </tr>
                  <tr className="hover:bg-[#151515]">
                    <td className="px-6 py-4"><code className="text-[#a78bfa]">custom</code></td>
                    <td className="px-6 py-4">Any Arbitrary API</td>
                    <td className="px-6 py-4 text-[#22c55e]">Configurable</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <p className="mt-4 text-sm text-[#6b7280]">
              <strong className="text-white">Selection Logic:</strong> The agent first uses LLM planning capabilities (Gemini / Claude) against connected tools. Fallbacks include a heuristic router that applies keyword and intent matching within <code className="text-[#a78bfa] bg-[#1c1c1c] px-1 py-0.5 rounded">tools.py</code>.
            </p>
          </section>

          <section>
            <h2 className="text-2xl font-bold text-white mb-6 tracking-tight">4. x402 Payment Protocol</h2>
            <Card className="border-[#1c1c1c] bg-[#0f0f0f]">
              <CardContent className="p-6">
                <p className="mb-4 text-sm text-[#8c94a3] leading-relaxed">
                  x402 is an HTTP-native micropayment standard built on the HTTP 402 Payment Required status code. Agents pay for API access per request — no subscriptions or API keys needed.
                </p>
                <div className="bg-[#111111] border border-[#1c1c1c] rounded-md p-4 font-mono text-xs overflow-x-auto text-[13px] leading-relaxed text-[#c1c8d4]">
                  <span className="text-[#7c3aed]"># Green wire execution — x402 payment + tool call</span><br/>
                  <span className="text-[#ec4899]">async def</span> <span className="text-[#3b82f6]">x402_pay</span>(url: <span className="text-[#10b981]">str</span>, amount_algo: <span className="text-[#10b981]">float</span>, wallet_key: <span className="text-[#10b981]">str</span>):<br/>
                  &nbsp;&nbsp;&nbsp;&nbsp;<span className="text-[#7c3aed]"># Step 1: Preflight</span><br/>
                  &nbsp;&nbsp;&nbsp;&nbsp;r = <span className="text-[#ec4899]">await</span> client.get(url)<br/>
                  &nbsp;&nbsp;&nbsp;&nbsp;<span className="text-[#ec4899]">if</span> r.status_code != 402:<br/>
                  &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;<span className="text-[#ec4899]">return</span> r.json()<br/><br/>
                  &nbsp;&nbsp;&nbsp;&nbsp;<span className="text-[#7c3aed]"># Step 2: Pay on Algorand</span><br/>
                  &nbsp;&nbsp;&nbsp;&nbsp;txid = <span className="text-[#ec4899]">await</span> algorand_client.pay(wallet_key, meta[<span className="text-[#eab308]">&apos;receiver&apos;</span>], amount_algo)<br/><br/>
                  &nbsp;&nbsp;&nbsp;&nbsp;<span className="text-[#7c3aed]"># Step 3: Retry</span><br/>
                  &nbsp;&nbsp;&nbsp;&nbsp;r2 = <span className="text-[#ec4899]">await</span> client.get(url, headers=&#123;<span className="text-[#eab308]">&apos;X-Payment-Response&apos;</span>: txid&#125;)<br/>
                  &nbsp;&nbsp;&nbsp;&nbsp;<span className="text-[#ec4899]">return</span> r2.json()
                </div>
              </CardContent>
            </Card>
          </section>

          <section>
            <h2 className="text-2xl font-bold text-white mb-6 tracking-tight">5. Security and Trust Model</h2>
            <div className="grid gap-6 sm:grid-cols-2">
              <div>
                <h3 className="font-semibold text-white mb-2">Graph-Level Access Control</h3>
                <p className="text-sm text-[#8c94a3] leading-relaxed">
                  Pipeline execution acts as hard access control. Agents can only call tools or speak to agents they are physically wired to in the canvas.
                </p>
              </div>
              <div>
                <h3 className="font-semibold text-white mb-2">Wallet Key Management</h3>
                <p className="text-sm text-[#8c94a3] leading-relaxed">
                  Keys are generated at deploy time. Production deployments integrate with MCP OS keychains instead of holding local memory files.
                </p>
              </div>
              <div>
                <h3 className="font-semibold text-white mb-2">Payment Verification</h3>
                <p className="text-sm text-[#8c94a3] leading-relaxed">
                  All x402 payments pass via the GoPlausible facilitator, neutralizing forgery, replays, and unverified API access.
                </p>
              </div>
              <div>
                <h3 className="font-semibold text-white mb-2">Pipeline Isolation</h3>
                <p className="text-sm text-[#8c94a3] leading-relaxed">
                  Distinct execution namespaces and per-deployment wallet silos ensure pipelines remain cleanly segregated.
                </p>
              </div>
            </div>
          </section>

        </div>
        
        <div className="mt-24 text-center border-t border-[#1c1c1c] pt-12">
          <ShinyButton onClick={() => router.push("/")} className="px-8">
            Back to App
          </ShinyButton>
        </div>
      </main>
    </div>
  );
}
