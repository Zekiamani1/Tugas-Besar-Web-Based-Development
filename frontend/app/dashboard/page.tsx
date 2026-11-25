import type { Metadata } from "next";
import { PageLayout } from "@/components/ui/PageLayout";
import { DatabaseList } from "@/features/dashboard/DatabaseList";

export const metadata: Metadata = {
  title: "Dashboard · Database Manager",
};

export default function DashboardPage() { 
  //fetch dulu databases nya
  return (
    <PageLayout subtitle="Database Manager" title="Dashboard">
      <DatabaseList databases={[]} />
    </PageLayout>
  );
}
