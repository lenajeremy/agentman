/**
 * What someone does on their computer before this phone can pair.
 *
 * The pairing screen is the first thing a new user sees, and "run am pair"
 * means nothing to someone who has not installed agentman yet. These are the
 * same steps as the site's /start page, in the order they are done; the last
 * one ends where the screen's Scan button takes over.
 */

export const SETUP_GUIDE_URL = "https://agentman-nu.vercel.app/start";

/** Works on macOS and Linux, and uses Homebrew when it is there. */
export const INSTALL_COMMAND = "curl -fsSL https://agentman-nu.vercel.app/install | sh";

export interface SetupStep {
  title: string;
  detail: string;
  /** Run on the computer; the phone offers to copy it. */
  command?: string;
}

export const SETUP_STEPS: readonly SetupStep[] = [
  {
    title: "Install agentman",
    detail: "On the Mac or Linux machine where your agents run, in a terminal.",
    command: INSTALL_COMMAND,
  },
  {
    title: "Connect your agents",
    detail: "Once. Your agents then tell Agentman when they finish or need you.",
    command: "am install-hooks",
  },
  {
    title: "Start Agentman",
    detail: "Leave it running in a terminal tab of its own.",
    command: "am serve",
  },
  {
    title: "Get a pairing code",
    detail: "In a second terminal. It shows a QR code and ten digits, for 60 seconds. Scan it with the button above.",
    command: "am pair",
  },
];
