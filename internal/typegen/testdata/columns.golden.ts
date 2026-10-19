export type Json =
  | string
  | number
  | boolean
  | null
  | { [key: string]: Json | undefined }
  | Json[]

export type Database = {
  audit: {
    Tables: {
      audit_log: {
        Row: {
          id: number
          note: string | null
        }
        Insert: {
          id?: number
          note?: string | null
        }
        Update: {
          id?: number
          note?: string | null
        }
        Relationships: []
      }
      z_last: {
        Row: {
          v: string
        }
        Insert: {
          v: string
        }
        Update: {
          v?: string
        }
        Relationships: []
      }
    }
    Views: {
      [_ in never]: never
    }
    Functions: {
      [_ in never]: never
    }
    Enums: {
      [_ in never]: never
    }
    CompositeTypes: {
      [_ in never]: never
    }
  }
  public: {
    Tables: {
      everything: {
        Row: {
          big_serial: number
          c_bigint: number | null
          c_bit: unknown | null
          c_bool: boolean | null
          c_bytea: string | null
          c_date: string | null
          c_double: number | null
          c_inet: string | null
          c_int_arr: number[]
          c_integer: number
          c_interval: unknown | null
          c_json: Json | null
          c_jsonb: Json
          c_money: string | null
          c_numeric: number
          c_point: unknown | null
          c_real: number | null
          c_smallint: number | null
          c_text: string | null
          c_text_arr: string[] | null
          c_time: unknown | null
          c_tstz: string | null
          c_tsvector: unknown | null
          c_uuid: string | null
          c_varchar: string
          created_at: string | null
          id: number
          opt_enum: "one" | "two" | null
          owner_id: string | null
          ref_id: number | null
          req_auto: string
          req_with_default: string
          small_serial: number
          status: "draft" | "published" | null
          updated_at: string | null
          weird:
            | 'say "hi"'
            | "line\nbreak"
            | "back\\slash"
            | ""
            | "it's"
            | "日本語"
        }
        Insert: {
          big_serial?: number
          c_bigint?: number | null
          c_bit?: unknown | null
          c_bool?: boolean | null
          c_bytea?: string | null
          c_date?: string | null
          c_double?: number | null
          c_inet?: string | null
          c_int_arr: number[]
          c_integer: number
          c_interval?: unknown | null
          c_json?: Json | null
          c_jsonb: Json
          c_money?: string | null
          c_numeric: number
          c_point?: unknown | null
          c_real?: number | null
          c_smallint?: number | null
          c_text?: string | null
          c_text_arr?: string[] | null
          c_time?: unknown | null
          c_tstz?: string | null
          c_tsvector?: unknown | null
          c_uuid?: string | null
          c_varchar: string
          created_at?: string | null
          id?: number
          opt_enum?: "one" | "two" | null
          owner_id?: string | null
          ref_id?: number | null
          req_auto: string
          req_with_default?: string
          small_serial?: number
          status?: "draft" | "published" | null
          updated_at?: string | null
          weird:
            | 'say "hi"'
            | "line\nbreak"
            | "back\\slash"
            | ""
            | "it's"
            | "日本語"
        }
        Update: {
          big_serial?: number
          c_bigint?: number | null
          c_bit?: unknown | null
          c_bool?: boolean | null
          c_bytea?: string | null
          c_date?: string | null
          c_double?: number | null
          c_inet?: string | null
          c_int_arr?: number[]
          c_integer?: number
          c_interval?: unknown | null
          c_json?: Json | null
          c_jsonb?: Json
          c_money?: string | null
          c_numeric?: number
          c_point?: unknown | null
          c_real?: number | null
          c_smallint?: number | null
          c_text?: string | null
          c_text_arr?: string[] | null
          c_time?: unknown | null
          c_tstz?: string | null
          c_tsvector?: unknown | null
          c_uuid?: string | null
          c_varchar?: string
          created_at?: never
          id?: number
          opt_enum?: "one" | "two" | null
          owner_id?: string | null
          ref_id?: number | null
          req_auto?: string
          req_with_default?: string
          small_serial?: number
          status?: "draft" | "published" | null
          updated_at?: string | null
          weird?:
            | 'say "hi"'
            | "line\nbreak"
            | "back\\slash"
            | ""
            | "it's"
            | "日本語"
        }
        Relationships: [
          {
            foreignKeyName: "everything_owner_id_fkey"
            columns: ["owner_id"]
            isOneToOne: false
            referencedRelation: "users"
            referencedColumns: ["id"]
          },
          {
            foreignKeyName: "everything_ref_id_fkey"
            columns: ["ref_id"]
            isOneToOne: false
            referencedRelation: "everything"
            referencedColumns: ["id"]
          },
        ]
      }
      membership: {
        Row: {
          org_id: number
          role: string
          user_id: string
        }
        Insert: {
          org_id: number
          role: string
          user_id: string
        }
        Update: {
          org_id?: number
          role?: string
          user_id?: string
        }
        Relationships: []
      }
      widths: {
        Row: {
          cjk79: "日日日日日日日日日日日日日日日日日日日日" | "ccccccccccccccc"
          cjk81:
            | "日日日日日日日日日日日日日日日日日日日日"
            | "ccccccccccccccccc"
          e79: "aaaaaaaaaaaaaaaaaaaaaaaaaaaa" | "bbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
          e80: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaa" | "bbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
          e81:
            | "aaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
            | "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
          nullable_enum:
            | "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
            | "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
            | "cccccccccc"
            | null
        }
        Insert: {
          cjk79: "日日日日日日日日日日日日日日日日日日日日" | "ccccccccccccccc"
          cjk81:
            | "日日日日日日日日日日日日日日日日日日日日"
            | "ccccccccccccccccc"
          e79: "aaaaaaaaaaaaaaaaaaaaaaaaaaaa" | "bbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
          e80: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaa" | "bbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
          e81:
            | "aaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
            | "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
          nullable_enum?:
            | "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
            | "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
            | "cccccccccc"
            | null
        }
        Update: {
          cjk79?: "日日日日日日日日日日日日日日日日日日日日" | "ccccccccccccccc"
          cjk81?:
            | "日日日日日日日日日日日日日日日日日日日日"
            | "ccccccccccccccccc"
          e79?: "aaaaaaaaaaaaaaaaaaaaaaaaaaaa" | "bbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
          e80?:
            | "aaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
            | "bbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
          e81?:
            | "aaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
            | "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
          nullable_enum?:
            | "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
            | "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
            | "cccccccccc"
            | null
        }
        Relationships: []
      }
      zero_fields: {
        Row: {}
        Insert: {}
        Update: {}
        Relationships: []
      }
    }
    Views: {
      [_ in never]: never
    }
    Functions: {
      [_ in never]: never
    }
    Enums: {
      [_ in never]: never
    }
    CompositeTypes: {
      [_ in never]: never
    }
  }
}

type DatabaseWithoutInternals = Omit<Database, "__InternalSupabase">

type DefaultSchema = DatabaseWithoutInternals[Extract<keyof Database, "public">]

export type Tables<
  DefaultSchemaTableNameOrOptions extends
    | keyof (DefaultSchema["Tables"] & DefaultSchema["Views"])
    | { schema: keyof DatabaseWithoutInternals },
  TableName extends DefaultSchemaTableNameOrOptions extends {
    schema: keyof DatabaseWithoutInternals
  }
    ? keyof (DatabaseWithoutInternals[DefaultSchemaTableNameOrOptions["schema"]]["Tables"] &
        DatabaseWithoutInternals[DefaultSchemaTableNameOrOptions["schema"]]["Views"])
    : never = never,
> = DefaultSchemaTableNameOrOptions extends {
  schema: keyof DatabaseWithoutInternals
}
  ? (DatabaseWithoutInternals[DefaultSchemaTableNameOrOptions["schema"]]["Tables"] &
      DatabaseWithoutInternals[DefaultSchemaTableNameOrOptions["schema"]]["Views"])[TableName] extends {
      Row: infer R
    }
    ? R
    : never
  : DefaultSchemaTableNameOrOptions extends keyof (DefaultSchema["Tables"] &
        DefaultSchema["Views"])
    ? (DefaultSchema["Tables"] &
        DefaultSchema["Views"])[DefaultSchemaTableNameOrOptions] extends {
        Row: infer R
      }
      ? R
      : never
    : never

export type TablesInsert<
  DefaultSchemaTableNameOrOptions extends
    | keyof DefaultSchema["Tables"]
    | { schema: keyof DatabaseWithoutInternals },
  TableName extends DefaultSchemaTableNameOrOptions extends {
    schema: keyof DatabaseWithoutInternals
  }
    ? keyof DatabaseWithoutInternals[DefaultSchemaTableNameOrOptions["schema"]]["Tables"]
    : never = never,
> = DefaultSchemaTableNameOrOptions extends {
  schema: keyof DatabaseWithoutInternals
}
  ? DatabaseWithoutInternals[DefaultSchemaTableNameOrOptions["schema"]]["Tables"][TableName] extends {
      Insert: infer I
    }
    ? I
    : never
  : DefaultSchemaTableNameOrOptions extends keyof DefaultSchema["Tables"]
    ? DefaultSchema["Tables"][DefaultSchemaTableNameOrOptions] extends {
        Insert: infer I
      }
      ? I
      : never
    : never

export type TablesUpdate<
  DefaultSchemaTableNameOrOptions extends
    | keyof DefaultSchema["Tables"]
    | { schema: keyof DatabaseWithoutInternals },
  TableName extends DefaultSchemaTableNameOrOptions extends {
    schema: keyof DatabaseWithoutInternals
  }
    ? keyof DatabaseWithoutInternals[DefaultSchemaTableNameOrOptions["schema"]]["Tables"]
    : never = never,
> = DefaultSchemaTableNameOrOptions extends {
  schema: keyof DatabaseWithoutInternals
}
  ? DatabaseWithoutInternals[DefaultSchemaTableNameOrOptions["schema"]]["Tables"][TableName] extends {
      Update: infer U
    }
    ? U
    : never
  : DefaultSchemaTableNameOrOptions extends keyof DefaultSchema["Tables"]
    ? DefaultSchema["Tables"][DefaultSchemaTableNameOrOptions] extends {
        Update: infer U
      }
      ? U
      : never
    : never

export type Enums<
  DefaultSchemaEnumNameOrOptions extends
    | keyof DefaultSchema["Enums"]
    | { schema: keyof DatabaseWithoutInternals },
  EnumName extends DefaultSchemaEnumNameOrOptions extends {
    schema: keyof DatabaseWithoutInternals
  }
    ? keyof DatabaseWithoutInternals[DefaultSchemaEnumNameOrOptions["schema"]]["Enums"]
    : never = never,
> = DefaultSchemaEnumNameOrOptions extends {
  schema: keyof DatabaseWithoutInternals
}
  ? DatabaseWithoutInternals[DefaultSchemaEnumNameOrOptions["schema"]]["Enums"][EnumName]
  : DefaultSchemaEnumNameOrOptions extends keyof DefaultSchema["Enums"]
    ? DefaultSchema["Enums"][DefaultSchemaEnumNameOrOptions]
    : never

export type CompositeTypes<
  PublicCompositeTypeNameOrOptions extends
    | keyof DefaultSchema["CompositeTypes"]
    | { schema: keyof DatabaseWithoutInternals },
  CompositeTypeName extends PublicCompositeTypeNameOrOptions extends {
    schema: keyof DatabaseWithoutInternals
  }
    ? keyof DatabaseWithoutInternals[PublicCompositeTypeNameOrOptions["schema"]]["CompositeTypes"]
    : never = never,
> = PublicCompositeTypeNameOrOptions extends {
  schema: keyof DatabaseWithoutInternals
}
  ? DatabaseWithoutInternals[PublicCompositeTypeNameOrOptions["schema"]]["CompositeTypes"][CompositeTypeName]
  : PublicCompositeTypeNameOrOptions extends keyof DefaultSchema["CompositeTypes"]
    ? DefaultSchema["CompositeTypes"][PublicCompositeTypeNameOrOptions]
    : never
