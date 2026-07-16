import { useState, type KeyboardEvent } from "react";
import React from "react";
import { X } from "lucide-react";
import { Box, HStack } from "@chakra-ui/react";

interface TagInputProps {
  value: string[];
  onChange: (tags: string[]) => void;
  placeholder?: string;
  suggestions?: string[];
}

export function TagInput({
  value,
  onChange,
  placeholder = "Type and press Enter...",
  suggestions,
}: TagInputProps) {
  const [input, setInput] = useState("");
  const [showSuggestions, setShowSuggestions] = useState(false);

  const filteredSuggestions =
    suggestions?.filter(
      (s) =>
        s.toLowerCase().includes(input.toLowerCase()) && !value.includes(s)
    ) ?? [];

  function addTag(tag: string) {
    const trimmed = tag.trim();
    if (trimmed && !value.includes(trimmed)) {
      onChange([...value, trimmed]);
    }
    setInput("");
    setShowSuggestions(false);
  }

  function removeTag(idx: number) {
    onChange(value.filter((_, i) => i !== idx));
  }

  function handleKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === "Enter" || e.key === ",") {
      e.preventDefault();
      addTag(input);
    } else if (e.key === "Backspace" && !input && value.length > 0) {
      removeTag(value.length - 1);
    }
  }

  return (
    <Box position="relative">
      <Box
        display="flex"
        flexWrap="wrap"
        gap="1.5"
        p="2"
        borderRadius="lg"
        borderWidth="1px"
        bg="bg"
        minH="40px"
        transition="colors"
        _focusWithin={{ borderColor: "fg" }}
      >
        {value.map((tag, i) => (
          <HStack
            key={i}
            as="span"
            display="inline-flex"
            alignItems="center"
            gap="1"
            px="2"
            py="0.5"
            borderRadius="md"
            borderWidth="1px"
            bg="bg.muted"
            color="fg"
            fontFamily="mono"
            fontSize="xs"
          >
            {tag}
            <button
              type="button"
              onClick={() => removeTag(i)}
              aria-label={`Remove ${tag}`}
              style={{ display: "flex", alignItems: "center", color: "inherit", cursor: "pointer", background: "none", border: "none", padding: 0 }}
            >
              <X size={12} />
            </button>
          </HStack>
        ))}
        <input
          type="text"
          value={input}
          onChange={(e) => {
            setInput(e.target.value);
            setShowSuggestions(true);
          }}
          onKeyDown={handleKeyDown}
          onFocus={() => setShowSuggestions(true)}
          onBlur={() => setTimeout(() => setShowSuggestions(false), 150)}
          placeholder={value.length === 0 ? placeholder : ""}
          style={{
            flex: 1,
            minWidth: "120px",
            background: "transparent",
            fontSize: "0.875rem",
            outline: "none",
            border: "none",
          }}
        />
      </Box>
      {showSuggestions && filteredSuggestions.length > 0 && (
        <Box
          position="absolute"
          zIndex="50"
          mt="1"
          w="full"
          maxH="40"
          overflowY="auto"
          borderRadius="lg"
          borderWidth="1px"
          bg="bg.panel"
          boxShadow="lg"
        >
          {filteredSuggestions.map((s) => (
            <button
              key={s}
              type="button"
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => addTag(s)}
              style={{
                display: "block",
                width: "100%",
                textAlign: "left",
                padding: "0.375rem 0.75rem",
                fontSize: "0.875rem",
                cursor: "pointer",
                background: "none",
                border: "none",
              }}
            >
              {s}
            </button>
          ))}
        </Box>
      )}
    </Box>
  );
}
