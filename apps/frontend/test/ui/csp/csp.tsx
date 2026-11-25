// A page with each component of src/components/ui. It is outside the production bundle (mode csp).
// The test opens each popup, so the styles of Base UI really apply under the CSP.
import { createRoot } from "react-dom/client";
import { NoInlineStyles } from "@/components/csp-provider";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@/components/ui/hover-card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { Trace } from "@/components/ui/trace";
import { Wordmark } from "@/components/wordmark";
import "@/styles.css";

function Page() {
  return (
    <NoInlineStyles>
      <TooltipProvider>
        <main className="flex flex-col items-start gap-4 p-4">
          <Wordmark className="h-8" />
          <Button>Plain button</Button>
          <Label htmlFor="name">Name</Label>
          <Input id="name" placeholder="Name" />
          <Label>
            <Checkbox aria-label="Accept" /> Accept
          </Label>
          <Switch aria-label="Enabled" />
          <Alert>
            <AlertTitle>Alert title</AlertTitle>
            <AlertDescription>Alert text</AlertDescription>
          </Alert>
          <Trace
            steps={[
              { id: "one", title: "One" },
              { id: "two", title: "Two" },
            ]}
            current={1}
          />

          <Dialog>
            <DialogTrigger render={<Button />}>Open dialog</DialogTrigger>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>Dialog title</DialogTitle>
                <DialogDescription>Dialog text</DialogDescription>
              </DialogHeader>
            </DialogContent>
          </Dialog>

          <Sheet>
            <SheetTrigger render={<Button />}>Open sheet</SheetTrigger>
            <SheetContent>
              <SheetHeader>
                <SheetTitle>Sheet title</SheetTitle>
                <SheetDescription>Sheet text</SheetDescription>
              </SheetHeader>
            </SheetContent>
          </Sheet>

          <HoverCard>
            <HoverCardTrigger render={<Button />}>Hover card</HoverCardTrigger>
            <HoverCardContent>Hover card text</HoverCardContent>
          </HoverCard>

          <Popover>
            <PopoverTrigger render={<Button />}>Open popover</PopoverTrigger>
            <PopoverContent>Popover text</PopoverContent>
          </Popover>

          <Select defaultValue="a" items={{ a: "Option A", b: "Option B" }}>
            <SelectTrigger aria-label="Choice">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="a">Option A</SelectItem>
              <SelectItem value="b">Option B</SelectItem>
            </SelectContent>
          </Select>

          <Tooltip>
            <TooltipTrigger render={<Button />}>Tooltip</TooltipTrigger>
            <TooltipContent>Tooltip text</TooltipContent>
          </Tooltip>

          <Collapsible>
            <CollapsibleTrigger render={<Button />}>Open collapsible</CollapsibleTrigger>
            <CollapsibleContent>Collapsible text</CollapsibleContent>
          </Collapsible>
        </main>
      </TooltipProvider>
    </NoInlineStyles>
  );
}

const root = document.getElementById("root");
if (root) createRoot(root).render(<Page />);
