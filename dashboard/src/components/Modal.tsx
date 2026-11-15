import { DialogFrame, type DialogFrameProps } from "./DialogFrame";

interface ModalProps extends DialogFrameProps {
  open: boolean;
}

/** Page-owned dialog: renders the shared frame while `open`. */
export function Modal({ open, ...frame }: ModalProps) {
  return open ? <DialogFrame {...frame} /> : null;
}
