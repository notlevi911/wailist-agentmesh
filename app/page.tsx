import { Hero } from "@/components/hero";
import { InfoSection } from "@/components/info-section";
import { WaitlistForm } from "@/components/waitlist-form";
import { FAQ } from "@/components/faq";
import { Footer } from "@/components/footer";

export default function Home() {
  return (
    <main>
      <Hero />
      <InfoSection />
      <WaitlistForm />
      <FAQ />
      <Footer />
    </main>
  );
}
