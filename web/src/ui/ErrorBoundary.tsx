import {Component, type ReactNode} from "react";
import {AlertTriangle, RefreshCw} from "lucide-react";

interface Props {
  children: ReactNode;
}

interface State {
  error?: Error;
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = {};

  static getDerivedStateFromError(error: Error): State {
    return {error};
  }

  componentDidCatch(): void {
    console.error("QCode Web projection failed");
  }

  render(): ReactNode {
    if (!this.state.error) {
      return this.props.children;
    }
    return (
      <main className="bootState" data-failed>
        <div className="bootMark"><AlertTriangle size={22} /></div>
        <h1>Web workspace unavailable</h1>
        <p>The browser projection could not be rendered safely.</p>
        <button className="recoveryButton" onClick={() => window.location.reload()}>
          <RefreshCw size={15} /> Reload
        </button>
      </main>
    );
  }
}
