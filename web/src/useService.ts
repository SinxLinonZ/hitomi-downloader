import { useEffect, useRef, useState } from "react";
import { api, Task } from "./api";

export function useService() {
  const [state, setState] = useState<
    "connecting" | "online" | "stopping" | "stopped"
  >("connecting");
  const [tasks, setTasks] = useState<Task[]>([]);
  const stream = useRef<EventSource | null>(null);
  const terminal = useRef(false);
  const stopPending = useRef(false);

  function finished() {
    terminal.current = true;
    stream.current?.close();
    setTasks((previous) =>
      previous.map((task) =>
        ["running", "queued"].includes(task.state)
          ? { ...task, state: "paused" }
          : task,
      ),
    );
    setState("stopped");
  }

  useEffect(() => {
    const events = new EventSource("/api/v1/events");
    stream.current = events;
    events.onmessage = (event) => {
      if (terminal.current) return;
      setTasks(JSON.parse(event.data));
      if (!stopPending.current) setState("online");
    };
    events.addEventListener("shutdown", () => {
      finished();
    });
    events.onerror = () => {
      if (!terminal.current && !stopPending.current) setState("connecting");
    };
    return () => {
      events.close();
      stream.current = null;
    };
  }, []);

  async function stop() {
    if (terminal.current || stopPending.current) return;
    stopPending.current = true;
    setState("stopping");
    try {
      await api("/shutdown", {});
      finished();
    } catch (error) {
      if (!terminal.current) {
        setState("connecting");
        throw error;
      }
    } finally {
      stopPending.current = false;
    }
  }

  async function refreshTasks() {
    const result = await api<Task[]>("/downloads");
    if (!terminal.current) setTasks(result);
  }

  return { state, tasks, stop, refreshTasks };
}
