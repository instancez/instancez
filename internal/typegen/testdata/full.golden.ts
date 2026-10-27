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
      audit_details: {
        Row: {
          entry_id: number | null
          id: number
        }
        Insert: {
          entry_id?: number | null
          id?: number
        }
        Update: {
          entry_id?: number | null
          id?: number
        }
        Relationships: [
          {
            foreignKeyName: "audit_details_entry_id_fkey"
            columns: ["entry_id"]
            isOneToOne: false
            referencedRelation: "audit_entries"
            referencedColumns: ["id"]
          },
        ]
      }
      audit_entries: {
        Row: {
          actor: string | null
          id: number
          note: string | null
          post_id: number | null
        }
        Insert: {
          actor?: string | null
          id?: number
          note?: string | null
          post_id?: number | null
        }
        Update: {
          actor?: string | null
          id?: number
          note?: string | null
          post_id?: number | null
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
      cats: {
        Row: {
          id: number
          name: string
        }
        Insert: {
          id?: number
          name: string
        }
        Update: {
          id?: number
          name?: string
        }
        Relationships: []
      }
      links: {
        Row: {
          a_id: number
          b_id: number
          multi_idx: number | null
          partial_idx: number | null
          plain_idx: number | null
          solo_idx: number | null
        }
        Insert: {
          a_id: number
          b_id: number
          multi_idx?: number | null
          partial_idx?: number | null
          plain_idx?: number | null
          solo_idx?: number | null
        }
        Update: {
          a_id?: number
          b_id?: number
          multi_idx?: number | null
          partial_idx?: number | null
          plain_idx?: number | null
          solo_idx?: number | null
        }
        Relationships: [
          {
            foreignKeyName: "links_a_id_fkey"
            columns: ["a_id"]
            isOneToOne: false
            referencedRelation: "cats"
            referencedColumns: ["id"]
          },
          {
            foreignKeyName: "links_multi_idx_fkey"
            columns: ["multi_idx"]
            isOneToOne: false
            referencedRelation: "cats"
            referencedColumns: ["id"]
          },
          {
            foreignKeyName: "links_partial_idx_fkey"
            columns: ["partial_idx"]
            isOneToOne: false
            referencedRelation: "cats"
            referencedColumns: ["id"]
          },
          {
            foreignKeyName: "links_plain_idx_fkey"
            columns: ["plain_idx"]
            isOneToOne: false
            referencedRelation: "cats"
            referencedColumns: ["id"]
          },
          {
            foreignKeyName: "links_solo_idx_fkey"
            columns: ["solo_idx"]
            isOneToOne: false
            referencedRelation: "cats"
            referencedColumns: ["id"]
          },
        ]
      }
      long_names: {
        Row: {
          cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc_:
            | number
            | null
          id: number
        }
        Insert: {
          cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc_?:
            | number
            | null
          id?: number
        }
        Update: {
          cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc_?:
            | number
            | null
          id?: number
        }
        Relationships: [
          {
            foreignKeyName: "long_names_cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc__fkey"
            columns: [
              "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc_",
            ]
            isOneToOne: false
            referencedRelation: "cats"
            referencedColumns: ["id"]
          },
        ]
      }
      posts: {
        Row: {
          author_id: string | null
          cat_id: number
          id: number
          parent_id: number | null
          tag_slug: string | null
        }
        Insert: {
          author_id?: string | null
          cat_id: number
          id?: number
          parent_id?: number | null
          tag_slug?: string | null
        }
        Update: {
          author_id?: string | null
          cat_id?: number
          id?: number
          parent_id?: number | null
          tag_slug?: string | null
        }
        Relationships: [
          {
            foreignKeyName: "posts_cat_id_fkey"
            columns: ["cat_id"]
            isOneToOne: false
            referencedRelation: "cats"
            referencedColumns: ["id"]
          },
          {
            foreignKeyName: "posts_parent_id_fkey"
            columns: ["parent_id"]
            isOneToOne: false
            referencedRelation: "posts"
            referencedColumns: ["id"]
          },
          {
            foreignKeyName: "posts_tag_slug_fkey"
            columns: ["tag_slug"]
            isOneToOne: false
            referencedRelation: "tags"
            referencedColumns: ["slug"]
          },
        ]
      }
      profiles: {
        Row: {
          fav_cat: number | null
          user_id: string
        }
        Insert: {
          fav_cat?: number | null
          user_id: string
        }
        Update: {
          fav_cat?: number | null
          user_id?: string
        }
        Relationships: [
          {
            foreignKeyName: "profiles_fav_cat_fkey"
            columns: ["fav_cat"]
            isOneToOne: false
            referencedRelation: "cats"
            referencedColumns: ["id"]
          },
        ]
      }
      tags: {
        Row: {
          slug: string
        }
        Insert: {
          slug: string
        }
        Update: {
          slug?: string
        }
        Relationships: []
      }
      users: {
        Row: {
          nickname: string | null
        }
        Insert: {
          nickname?: string | null
        }
        Update: {
          nickname?: string | null
        }
        Relationships: []
      }
    }
    Views: {
      [_ in never]: never
    }
    Functions: {
      add_one: {
        Args: {
          n?: number
        }
        Returns: number
      }
      all_cats: {
        Args: Record<PropertyKey, never>
        Returns: Database["public"]["Tables"]["cats"]["Row"][]
        SetofOptions: {
          from: "*"
          to: "cats"
          isOneToOne: false
          isSetofReturn: true
        }
      }
      composite: {
        Args: Record<PropertyKey, never>
        Returns: unknown
      }
      greet: {
        Args: {
          bare: string
          forced: string
          loud?: boolean
          nullable?: string
          who: string
        }
        Returns: string
      }
      ints: {
        Args: Record<PropertyKey, never>
        Returns: number[]
      }
      long_table_col: {
        Args: Record<PropertyKey, never>
        Returns: {
          llllllllllllllllllllllllllllllllllllllllllllllllllllllllllllll:
            | number
            | null
          short: string | null
        }[]
      }
      mystery_rows: {
        Args: Record<PropertyKey, never>
        Returns: Record<string, unknown>[]
      }
      ping: {
        Args: Record<PropertyKey, never>
        Returns: undefined
      }
      public_cats: {
        Args: Record<PropertyKey, never>
        Returns: Database["public"]["Tables"]["cats"]["Row"][]
        SetofOptions: {
          from: "*"
          to: "cats"
          isOneToOne: false
          isSetofReturn: true
        }
      }
      qualified: {
        Args: Record<PropertyKey, never>
        Returns: unknown
      }
      rec: {
        Args: Record<PropertyKey, never>
        Returns: unknown
      }
      recent_audit: {
        Args: Record<PropertyKey, never>
        Returns: Database["audit"]["Tables"]["audit_entries"]["Row"][]
        SetofOptions: {
          from: "*"
          to: "audit_entries"
          isOneToOne: false
          isSetofReturn: true
        }
      }
      rrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrr: {
        Args: Record<PropertyKey, never>
        Returns: {
          rrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrr:
            | number
            | null
        }[]
      }
      some_ids: {
        Args: Record<PropertyKey, never>
        Returns: {
          some_ids: number | null
        }[]
      }
      stats: {
        Args: Record<PropertyKey, never>
        Returns: {
          a: number | null
          b: number | null
          c: string | null
          d: unknown | null
        }[]
      }
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

export const Constants = {
  audit: {
    Enums: {},
  },
  public: {
    Enums: {},
  },
} as const
