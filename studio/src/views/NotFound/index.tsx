import { ExclamationTriangleIcon } from "@radix-ui/react-icons";
import { Link, useLocation } from "wouter";

import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { PageHeader } from "@/components/ui/PageHeader";

// NotFound answers an address no studio route declares. The router used to
// fall through to Home, so a typo (/studio/settings) looked like a working
// page while the address bar kept the wrong path. This page says the address
// is unknown, echoes it, and offers the two ways back: Home and the Sitemap.
export default function NotFound() {
  const [location] = useLocation();
  return (
    <div className="flex h-full min-h-0 flex-col bg-surface-0 text-fg-default">
      <PageHeader
        icon={<ExclamationTriangleIcon className="h-5 w-5" />}
        title="Page not found"
        description={
          <>
            There is no page at <code className="font-mono">{location}</code>.
          </>
        }
      />
      <div className="flex flex-1 items-center justify-center p-6">
        <EmptyState
          title="This address doesn't exist in the studio."
          message="Check the address for a typo, or pick a page from the Sitemap."
          action={
            <Link href="/">
              <Button variant="primary" size="sm">
                Go to Home
              </Button>
            </Link>
          }
          secondaryAction={
            <Link href="/sitemap">
              <Button variant="secondary" size="sm">
                Open Sitemap
              </Button>
            </Link>
          }
        />
      </div>
    </div>
  );
}
