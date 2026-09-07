import { Suspense } from "react";
import { BazaarPage } from "@/components/bazaar/BazaarPage";

export default function Page() {
  return (
    <Suspense fallback={null}>
      <BazaarPage />
    </Suspense>
  );
}
